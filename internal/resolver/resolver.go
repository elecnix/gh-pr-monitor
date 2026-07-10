package resolver

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	pullURLRE = regexp.MustCompile(`^/([^/]+)/([^/]+)/pull/([0-9]+)(?:/.*)?$`)
)

// Identity represents a fully-resolved reference target.
// Target is one of: "pr", "ref", "commit", "issue".
type Identity struct {
	Owner     string
	Repo      string
	Host      string
	Number    int    // PR number or issue number
	Target    string // "pr" | "ref" | "commit" | "issue" | "run"
	Ref       string // branch ref for "ref" target
	CommitSHA string // commit SHA for "commit" target
	RunID     int    // workflow run id for "run" target
}

// NormalizeSelector ensures that either an explicit selector or --pr flag is present and mutually consistent.
func NormalizeSelector(selector string, prFlag int) (string, error) {
	selector = strings.TrimSpace(selector)

	switch {
	case selector != "" && prFlag > 0:
		if !matchesNumber(selector, prFlag) {
			return "", fmt.Errorf("pull request argument %q does not match --pr=%d", selector, prFlag)
		}
	case selector == "" && prFlag > 0:
		selector = strconv.Itoa(prFlag)
	}

	if selector == "" {
		return "", errors.New("must specify a pull request via --pr or selector")
	}

	if isNumeric(selector) {
		return selector, nil
	}

	if _, err := parsePullURL(selector); err == nil {
		return selector, nil
	}

	return "", fmt.Errorf("invalid pull request selector %q: must be a pull request URL or number", selector)
}

// Resolve interprets a selector, optional repo flag, and host (GH_HOST) into a concrete pull request identity.
func Resolve(selector, repoFlag, host string) (Identity, error) {
	selector = strings.TrimSpace(selector)
	repoFlag = strings.TrimSpace(repoFlag)
	host = SanitizeHost(host)

	if selector == "" {
		return Identity{}, errors.New("empty selector")
	}

	if id, err := parsePullURL(selector); err == nil {
		id.Target = "pr"
		return id, nil
	}

	if n, err := strconv.Atoi(selector); err == nil && n > 0 {
		owner, repo, err := splitRepo(repoFlag)
		if err != nil {
			return Identity{}, fmt.Errorf("--repo: %w", err)
		}
		return Identity{Owner: owner, Repo: repo, Host: host, Number: n, Target: "pr"}, nil
	}

	return Identity{}, fmt.Errorf("invalid pull request selector: %q", selector)
}

// ResolveRef resolves a ref (branch) target for monitoring.
func ResolveRef(ref, repoFlag, host string) (Identity, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Identity{}, errors.New("ref must be non-empty")
	}
	owner, repo, err := splitRepo(repoFlag)
	if err != nil {
		return Identity{}, fmt.Errorf("--repo: %w", err)
	}
	return Identity{Owner: owner, Repo: repo, Host: SanitizeHost(host), Ref: ref, Target: "ref"}, nil
}

// ResolveCommit resolves a commit SHA target for monitoring.
func ResolveCommit(sha, repoFlag, host string) (Identity, error) {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return Identity{}, errors.New("commit SHA must be non-empty")
	}
	owner, repo, err := splitRepo(repoFlag)
	if err != nil {
		return Identity{}, fmt.Errorf("--repo: %w", err)
	}
	return Identity{Owner: owner, Repo: repo, Host: SanitizeHost(host), CommitSHA: sha, Target: "commit"}, nil
}

// ResolveRun resolves a GitHub Actions workflow-run target for monitoring.
// The run id is the numeric id from a run's URL (e.g. .../actions/runs/<id>).
func ResolveRun(runID int, repoFlag, host string) (Identity, error) {
	if runID <= 0 {
		return Identity{}, errors.New("run id must be positive")
	}
	owner, repo, err := splitRepo(repoFlag)
	if err != nil {
		return Identity{}, fmt.Errorf("--repo: %w", err)
	}
	return Identity{Owner: owner, Repo: repo, Host: SanitizeHost(host), RunID: runID, Target: "run"}, nil
}

// ResolveRepo resolves a repository target for monitoring (watches for new PRs
// and issues).
func ResolveRepo(repoFlag, host string) (Identity, error) {
	owner, repo, err := splitRepo(repoFlag)
	if err != nil {
		return Identity{}, fmt.Errorf("--repo: %w", err)
	}
	return Identity{Owner: owner, Repo: repo, Host: SanitizeHost(host), Target: "repo"}, nil
}

// ResolveIssue resolves an issue target for monitoring.
func ResolveIssue(number int, repoFlag, host string) (Identity, error) {
	if number <= 0 {
		return Identity{}, errors.New("issue number must be positive")
	}
	owner, repo, err := splitRepo(repoFlag)
	if err != nil {
		return Identity{}, fmt.Errorf("--repo: %w", err)
	}
	return Identity{Owner: owner, Repo: repo, Host: SanitizeHost(host), Number: number, Target: "issue"}, nil
}

func parsePullURL(raw string) (Identity, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Identity{}, err
	}
	if u.Host == "" {
		return Identity{}, errors.New("missing host")
	}
	matches := pullURLRE.FindStringSubmatch(u.Path)
	if matches == nil {
		return Identity{}, errors.New("not a pull request url")
	}
	number, _ := strconv.Atoi(matches[3])
	return Identity{
		Owner:  matches[1],
		Repo:   matches[2],
		Host:   SanitizeHost(u.Host),
		Number: number,
	}, nil
}

func matchesNumber(selector string, target int) bool {
	if id, err := parsePullURL(selector); err == nil {
		return id.Number == target
	}
	if n, err := strconv.Atoi(selector); err == nil {
		return n == target
	}
	return false
}

func isNumeric(selector string) bool {
	if selector == "" {
		return false
	}
	for _, r := range selector {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func splitRepo(repoFlag string) (string, string, error) {
	if repoFlag == "" {
		return "", "", errors.New("no repository specified: use --repo owner/repo, or run from a git repository to infer automatically")
	}
	parts := strings.Split(repoFlag, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("expected owner/repo")
	}
	return parts[0], parts[1], nil
}

// SanitizeHost normalizes a host string by stripping scheme, path, and port.
// Returns "github.com" if the result is empty.
func SanitizeHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "github.com"
	}

	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			raw = u.Host
		} else {
			raw = strings.TrimPrefix(strings.TrimPrefix(lower, "http://"), "https://")
		}
	}

	if strings.Contains(raw, "/") {
		raw = strings.SplitN(raw, "/", 2)[0]
	}

	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	} else if idx := strings.Index(raw, ":"); idx >= 0 {
		raw = raw[:idx]
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "github.com"
	}

	return strings.ToLower(raw)
}
