// Package tstat provides utilities for parsing information from Go test runs or code coverage.
package tstat

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/nickfiggins/tstat/internal/gocover"
	"github.com/nickfiggins/tstat/internal/gofunc"
	"github.com/nickfiggins/tstat/internal/gotest"
	"golang.org/x/tools/cover"
)

// Cover parses the coverage and function profiles and returns a statistics based on the profiles read.
// The corresponding function profile will be generated automatically. If you want to provide an existing
// function profile, use CoverFromReaders. The cover profile must be a valid coverage profile generated by
// `go test -coverprofile=cover.out`.
func Cover(coverProfile string, opts ...CoverOpt) (Coverage, error) {
	covOut, err := os.ReadFile(coverProfile)
	if err != nil {
		return Coverage{}, fmt.Errorf("error reading coverage profile: %w", err)
	}
	fnOut, err := runFuncCover(coverProfile)
	if err != nil {
		return Coverage{}, err
	}

	cp := NewCoverageParser(opts...)
	return cp.Stats(bytes.NewBuffer(covOut), bytes.NewBuffer(fnOut))
}

// CoverFromReaders parses the coverage and function profiles and returns a statistics based on the profiles read.
// If you want to generate the function profile automatically, use Cover instead.
func CoverFromReaders(coverProfile io.Reader, fnProfile io.Reader, opts ...CoverOpt) (Coverage, error) {
	if coverProfile == nil {
		return Coverage{}, errors.New("cover profile must not be nil")
	}

	if fnProfile == nil {
		return Coverage{}, errors.New("function profile must not be nil")
	}

	cp := NewCoverageParser(opts...)
	return cp.Stats(coverProfile, fnProfile)
}

// CoverageParser is a parser for coverage profiles that can be configured to read from files or io.Readers.
// If only a cover profile is provided, the corresponding function profile will be generated automatically.
// If a function profile is provided, it will be used instead of generating one - which is useful when parsing profiles
// that aren't part of the current project.
type CoverageParser struct {
	trimModule string

	coverParser func(io.Reader) ([]*gocover.PackageStatements, error)
	funcParser  func(io.Reader) (gofunc.Output, error)
}

// NewCoverageParser returns a new CoverageParser with the given options.
func NewCoverageParser(opts ...CoverOpt) *CoverageParser {
	parser := &CoverageParser{
		coverParser: func(r io.Reader) ([]*gocover.PackageStatements, error) {
			profs, err := cover.ParseProfilesFromReader(r)
			if err != nil {
				return nil, fmt.Errorf("couldn't parse cover profile: %w", err)
			}
			return gocover.ByPackage(profs), nil
		},
		funcParser: gofunc.Read,
	}

	for _, opt := range opts {
		opt(parser)
	}

	return parser
}

// CoverOpt is a functional option for configuring a CoverageParser.
type CoverOpt func(*CoverageParser)

// WithRootModule sets the root module to trim from the file names in the coverage profile.
func WithRootModule(module string) CoverOpt {
	return func(cp *CoverageParser) {
		cp.trimModule = filepath.Clean(module)
	}
}

// Stats parses the coverage and function profiles and returns a statistics based on the profiles read.
func (p *CoverageParser) Stats(coverProfile, fnProfile io.Reader) (Coverage, error) {
	profiles, err := p.coverParser(coverProfile)
	if err != nil {
		return Coverage{}, fmt.Errorf("couldn't parse cover profile: %w", err)
	}

	output, err := p.funcParser(fnProfile)
	if err != nil {
		return Coverage{}, fmt.Errorf("couldn't parse func profile: %w", err)
	}

	coverage := newCoverage(profiles, output)

	return *coverage, nil
}

// TestParser is a parser for test output JSON.
type TestParser struct {
	testParser func(io.Reader) ([]gotest.Event, error)
}

// NewTestParser returns a new TestParser.
func NewTestParser() *TestParser {
	return &TestParser{testParser: gotest.ReadJSON}
}

// TestsFromReader parses the test output JSON from a reader and returns a TestRun based on the output read.
func TestsFromReader(outJSON io.Reader) (TestRun, error) {
	tp := NewTestParser()
	return tp.Stats(outJSON)
}

// Tests parses the test output JSON file and returns a TestRun based on the output read.
func Tests(outFile string) (TestRun, error) {
	b, err := os.ReadFile(outFile)
	if err != nil {
		return TestRun{}, fmt.Errorf("couldn't read file: %w", err)
	}
	tp := NewTestParser()
	return tp.Stats(bytes.NewBuffer(b))
}

// Stats parses the test output and returns a TestRun based on the output read.
func (p *TestParser) Stats(outJSON io.Reader) (TestRun, error) {
	out, err := p.testParser(outJSON)
	if err != nil {
		return TestRun{}, err
	}

	return parseTestOutputs(out)
}

// runFuncCover runs `go tool cover -func=<cover profile>` and returns the output.
func runFuncCover(profile string) ([]byte, error) {
	goTool := filepath.Join(runtime.GOROOT(), "bin/go")
	cmd := exec.Command(goTool, "tool", "cover", fmt.Sprintf("-func=%v", profile))
	fnProfile, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("couldn't get function coverage: %w", handleExecError(err))
	}
	return fnProfile, nil
}

func handleExecError(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w, stderr %v", err, string(ee.Stderr))
	}
	return err
}
