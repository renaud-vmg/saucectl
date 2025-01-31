package sauceignore

import (
	"bufio"
	"github.com/saucelabs/saucectl/internal/msg"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const commentPrefix = "#"

// patternsFromFile reads .sauceignore file and creates ignore patters if .sauceignore file is exists.
func patternsFromFile(path string) ([]Pattern, error) {
	fPath := filepath.Join(path)
	f, err := os.Open(fPath)
	if err != nil {
		// In case .sauceignore file doesn't exists.
		if os.IsNotExist(err) {
			msg.LogSauceIgnoreNotExist()
			return []Pattern{}, nil
		}
		return []Pattern{}, err
	}
	defer f.Close()

	var ps []Pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		s := scanner.Text()
		if !strings.HasPrefix(s, commentPrefix) && len(strings.TrimSpace(s)) > 0 {
			ps = append(ps, NewPattern(s))
		}
	}

	return ps, nil
}

// Pattern defines a single sauceignore pattern.
type Pattern struct {
	P string
}

// NewPattern create new Pattern.
func NewPattern(p string) Pattern {
	return Pattern{P: p}
}

func convPtrnsToGitignorePtrns(pp []Pattern) []gitignore.Pattern {
	res := make([]gitignore.Pattern, len(pp))
	for i := 0; i < len(pp); i++ {
		res[i] = gitignore.ParsePattern(pp[i].P, nil)
	}

	return res
}

// Matcher defines matcher for sauceignore patterns.
type Matcher interface {
	Match(path []string, isDir bool) bool
}

type matcher struct {
	matcher gitignore.Matcher
}

// Match matches patterns.
func (m *matcher) Match(path []string, isDir bool) bool {
	return m.matcher.Match(path, isDir)
}

// NewMatcher constructs a new matcher.
func NewMatcher(ps []Pattern) Matcher {
	gps := convPtrnsToGitignorePtrns(ps)
	return &matcher{matcher: gitignore.NewMatcher(gps)}
}

// NewMatcherFromFile constructs a new matcher from file.
func NewMatcherFromFile(path string) (Matcher, error) {
	ps, err := patternsFromFile(path)
	if err != nil {
		return nil, err
	}

	return NewMatcher(ps), nil
}
