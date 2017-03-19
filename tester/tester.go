package tester

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"crypto/md5"

	"github.com/dave/courtney/shared"
	"github.com/dave/courtney/tester/merge"
	"github.com/dave/patsy/pathcache"
	"github.com/dave/patsy/vos"
	"github.com/pkg/errors"
	"golang.org/x/tools/cover"
)

func New(env vos.Env, paths *pathcache.PathCache) *Tester {
	t := &Tester{
		env:   env,
		paths: paths,
	}
	t.previousWd, _ = t.env.Getwd()
	return t
}

type Tester struct {
	env        vos.Env
	cover      string
	Results    []*cover.Profile
	previousWd string
	paths      *pathcache.PathCache
}

func (t *Tester) Test(packages []shared.PackageSpec) error {

	var err error
	if t.cover, err = ioutil.TempDir("", "coverage"); err != nil {
		return errors.Wrap(err, "Error creating temporary coverage dir")
	}
	defer os.RemoveAll(t.cover)

	for _, spec := range packages {
		if err := t.processDir(spec.Dir, packages); err != nil {
			return err
		}
	}

	return nil
}

func (t *Tester) Save() error {
	if len(t.Results) == 0 {
		fmt.Println("No results")
		return nil
	}
	f, err := os.Create(filepath.Join(t.previousWd, "coverage.out"))
	if err != nil {
		return errors.Wrapf(err, "Error creating output coverage file coverage.out")
	}
	defer f.Close()
	merge.DumpProfiles(t.Results, f)
	return nil
}

func (t *Tester) ProcessExcludes(excludes map[string]map[int]bool) error {
	var processed []*cover.Profile

	for _, p := range t.Results {

		// Filenames in t.Results are in go package form. We need to convert to
		// filepaths before use
		fpath, err := t.paths.GoNameToFilePath(p.FileName)
		if err != nil {
			return err
		}

		f, ok := excludes[fpath]
		if !ok {
			// no excludes in this file - add the profile unchanged
			processed = append(processed, p)
			continue
		}
		var blocks []cover.ProfileBlock
		for _, b := range p.Blocks {
			excluded := false
			for line := b.StartLine; line <= b.EndLine; line++ {
				if ex, ok := f[line]; ok && ex {
					excluded = true
					break
				}
			}
			if !excluded || b.Count > 0 {
				// include blocks that are not excluded
				// also include any blocks that have coverage
				blocks = append(blocks, b)
			}
		}
		profile := &cover.Profile{
			FileName: p.FileName,
			Mode:     p.Mode,
			Blocks:   blocks,
		}
		processed = append(processed, profile)
	}
	t.Results = processed
	return nil
}

func (t *Tester) processDir(dir string, all []shared.PackageSpec) error {

	coverageFilename := fmt.Sprintf("%x", md5.Sum([]byte(dir))) + ".out"
	coverageFilepath := filepath.Join(t.cover, coverageFilename)

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return errors.Wrapf(err, "Error reading files from %s", dir)
	}

	foundTest := false
	for _, f := range files {
		if strings.HasSuffix(f.Name(), "_test.go") {
			foundTest = true
		}
	}
	if !foundTest {
		return nil
	}

	os.Chdir(dir)

	var allpkgs []string
	for _, s := range all {
		allpkgs = append(allpkgs, s.Path)
	}
	coverParam := fmt.Sprintf("-coverpkg=%s", strings.Join(allpkgs, ","))
	outParam := fmt.Sprintf("-coverprofile=%s", coverageFilepath)
	exe := exec.Command("go", "test", outParam, coverParam)
	exe.Env = t.env.Environ()
	b, err := exe.CombinedOutput()
	if strings.Contains(string(b), "no buildable Go source files in") {
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "Error executing test \nOutput:[\n%s]\n", b)
	}
	return t.processCoverageFile(coverageFilepath)
}

func (t *Tester) processCoverageFile(filename string) error {
	profiles, err := cover.ParseProfiles(filename)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		if t.Results, err = merge.AddProfile(t.Results, p); err != nil {
			return err
		}
	}
	return nil
}
