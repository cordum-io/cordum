package ci

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// GitLabConfig configures a GitLab CI scanner. The scanner is observe-
// only: it issues GET requests against the GitLab read API only.
type GitLabConfig struct {
	// BaseURL is the GitLab API root, e.g. `https://gitlab.com`. The
	// scanner adds `/api/v4` itself. For tests, point this at an
	// httptest.Server.URL.
	BaseURL string
	// Token is a GitLab personal access token / project access token
	// / OAuth token with at most `read_api` scope.
	Token string
	// Projects is the operator-supplied set of `<group>/<project>`
	// paths to poll. Empty == nothing scanned.
	Projects []string
	// HTTPClient is the *http.Client to use. Nil falls back to a
	// timeout-bounded default.
	HTTPClient *http.Client
}

type gitlabScanner struct {
	cfg    GitLabConfig
	http   *httpReader
	httpMu sync.Mutex
}

// NewGitLabScanner returns a GitLab CI scanner.
func NewGitLabScanner(cfg GitLabConfig) ProviderScanner {
	return &gitlabScanner{cfg: cfg}
}

func (s *gitlabScanner) Provider() Provider { return ProviderGitLab }

func (s *gitlabScanner) ensureHTTP() error {
	s.httpMu.Lock()
	defer s.httpMu.Unlock()
	if s.http != nil {
		return nil
	}
	if strings.TrimSpace(s.cfg.BaseURL) == "" {
		return fmt.Errorf("gitlab scanner: BaseURL is required")
	}
	r, err := newHTTPReader(s.cfg.BaseURL, s.cfg.HTTPClient)
	if err != nil {
		return fmt.Errorf("gitlab scanner: %w", err)
	}
	s.http = r.withTokenHeader("PRIVATE-TOKEN", s.cfg.Token)
	return nil
}

// Scan walks each configured project, lists pipelines + jobs, and
// emits findings through the shared detector pipeline.
func (s *gitlabScanner) Scan(ctx context.Context, d *Detector) error {
	if len(s.cfg.Projects) == 0 {
		return nil
	}
	if err := s.ensureHTTP(); err != nil {
		return err
	}
	var firstErr error
	for _, proj := range s.cfg.Projects {
		if err := s.scanProject(ctx, d, proj); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type gitlabProject struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	DefaultBranch     string `json:"default_branch"`
}

type gitlabPipeline struct {
	ID     int    `json:"id"`
	SHA    string `json:"sha"`
	Ref    string `json:"ref"`
	Status string `json:"status"`
	Source string `json:"source"`
	User   struct {
		Username string `json:"username"`
	} `json:"user"`
}

type gitlabJob struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Stage  string `json:"stage"`
	Runner struct {
		ID          int      `json:"id"`
		Description string   `json:"description"`
		TagList     []string `json:"tag_list"`
	} `json:"runner"`
}

type gitlabVariable struct {
	Key string `json:"key"`
}

func (s *gitlabScanner) scanProject(ctx context.Context, d *Detector, path string) error {
	project, err := s.fetchProject(ctx, path)
	if err != nil {
		return err
	}
	if project == nil {
		return nil
	}
	pipelines, err := s.fetchPipelines(ctx, project.ID)
	if err != nil {
		return err
	}
	varNames := s.fetchVariableNames(ctx, project.ID)
	workflowYAML := s.fetchPipelineYAML(ctx, project.ID, project.DefaultBranch)
	usesActions, runCommands := parseGitLabCIYAML(workflowYAML)

	for _, p := range pipelines {
		jobs, err := s.fetchJobs(ctx, project.ID, p.ID)
		if err != nil {
			continue
		}
		labels := []string{}
		var runnerID, jobID string
		for _, j := range jobs {
			if j.Runner.ID != 0 {
				runnerID = strconv.Itoa(j.Runner.ID)
				labels = append(labels, j.Runner.TagList...)
				labels = append(labels, j.Runner.Description)
			}
			if jobID == "" {
				jobID = strconv.Itoa(j.ID)
			}
		}
		run := Run{
			Provider:     ProviderGitLab,
			Workspace:    strings.SplitN(project.PathWithNamespace, "/", 2)[0],
			Repo:         project.PathWithNamespace,
			Ref:          p.Ref,
			HeadSHA:      p.SHA,
			RunID:        strconv.Itoa(p.ID),
			JobID:        jobID,
			WorkflowID:   strconv.Itoa(project.ID),
			RunnerID:     runnerID,
			Event:        p.Source,
			Actor:        p.User.Username,
			Labels:       labels,
			EnvNames:     varNames,
			UsesActions:  usesActions,
			RunCommands:  runCommands,
			WorkflowPath: ".gitlab-ci.yml",
			WorkflowYAML: workflowYAML,
			IsScheduled:  strings.EqualFold(p.Source, "schedule"),
			IsForkPR:     strings.EqualFold(p.Source, "external_pull_request_event"),
		}
		if err := d.EmitRun(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (s *gitlabScanner) fetchProject(ctx context.Context, path string) (*gitlabProject, error) {
	escaped := url.PathEscape(path)
	var p gitlabProject
	status, _, err := s.http.get(ctx, "/api/v4/projects/"+escaped, &p)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound || p.ID == 0 {
		return nil, nil
	}
	return &p, nil
}

func (s *gitlabScanner) fetchPipelines(ctx context.Context, projectID int) ([]gitlabPipeline, error) {
	var pipelines []gitlabPipeline
	rel := fmt.Sprintf("/api/v4/projects/%d/pipelines", projectID)
	_, _, err := s.http.get(ctx, rel, &pipelines)
	if err != nil {
		return nil, err
	}
	if len(pipelines) > MaxRunsPerScan {
		pipelines = pipelines[:MaxRunsPerScan]
	}
	return pipelines, nil
}

func (s *gitlabScanner) fetchJobs(ctx context.Context, projectID, pipelineID int) ([]gitlabJob, error) {
	var jobs []gitlabJob
	rel := fmt.Sprintf("/api/v4/projects/%d/pipelines/%d/jobs", projectID, pipelineID)
	_, _, err := s.http.get(ctx, rel, &jobs)
	return jobs, err
}

// fetchVariableNames reads NAMES only. The GitLab variables endpoint
// returns `value` when the API token has sufficient scope, but we
// throw it away — only `key` is retained.
func (s *gitlabScanner) fetchVariableNames(ctx context.Context, projectID int) []string {
	var vars []gitlabVariable
	rel := fmt.Sprintf("/api/v4/projects/%d/variables", projectID)
	_, _, err := s.http.get(ctx, rel, &vars)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		if v.Key != "" {
			out = append(out, v.Key)
		}
	}
	return out
}

// fetchPipelineYAML reads `.gitlab-ci.yml` from the project's default
// branch via the raw-file API. Returns empty string when the file is
// absent or the request errors — signal extractors degrade gracefully.
func (s *gitlabScanner) fetchPipelineYAML(ctx context.Context, projectID int, ref string) string {
	branch := strings.TrimSpace(ref)
	if branch == "" {
		branch = "main"
	}
	rel := fmt.Sprintf("/api/v4/projects/%d/repository/files/.gitlab-ci.yml/raw?ref=%s",
		projectID, url.QueryEscape(branch))
	status, body, err := s.http.getRaw(ctx, rel)
	if err != nil || status == http.StatusNotFound {
		return ""
	}
	return string(body)
}

// parseGitLabCIYAML extracts `image:` references (treated as `uses:`
// for action-match purposes) and `script:` leading tokens from a
// `.gitlab-ci.yml` body. The parser is deliberately tolerant — bad
// YAML returns empty slices, no panic.
//
// Per §8.5 we NEVER persist any value beyond the leading token.
func parseGitLabCIYAML(yaml string) ([]string, []string) {
	if yaml == "" {
		return nil, nil
	}
	var uses, runs []string
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "image:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))
			val = strings.Trim(val, "\"'")
			if val != "" {
				uses = append(uses, val)
			}
		}
		if strings.HasPrefix(trimmed, "- ") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			tok := firstShellToken(val)
			if tok != "" {
				runs = append(runs, tok)
			}
		}
	}
	return uses, runs
}

// firstShellToken returns the leading word of a shell command, dropped
// of common quoting / inline-env prefixes. Result MUST be a bare
// identifier — never a value, never a path.
func firstShellToken(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	for strings.HasPrefix(line, "$") || strings.HasPrefix(line, "(") {
		line = line[1:]
	}
	fields := strings.Fields(line)
	for _, f := range fields {
		// Drop inline `KEY=value` prefixes; bash treats those as env
		// assignments, not the command.
		if strings.Contains(f, "=") {
			continue
		}
		return strings.Trim(f, "\"'`")
	}
	return ""
}
