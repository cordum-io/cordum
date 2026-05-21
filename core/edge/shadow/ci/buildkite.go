package ci

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// BuildkiteConfig configures a Buildkite scanner. Read-only against the
// Buildkite v2 REST API (`/v2/organizations/<org>/pipelines/<slug>/...`).
type BuildkiteConfig struct {
	// BaseURL is the Buildkite API root, e.g. `https://api.buildkite.com`.
	BaseURL string
	// Token is a Buildkite API access token with read-only scopes
	// (`read_pipelines`, `read_builds`, `read_pipeline_files`).
	Token string
	// Organizations lists slugs to walk. Empty == nothing scanned.
	Organizations []string
	// Pipelines is an optional explicit `<org>/<pipeline-slug>` filter.
	// When non-empty, only those pipelines are scanned and the
	// Organizations list is ignored.
	Pipelines []string
	// HTTPClient is the *http.Client to use. Nil falls back to a
	// timeout-bounded default.
	HTTPClient *http.Client
}

type buildkiteScanner struct {
	cfg    BuildkiteConfig
	http   *httpReader
	httpMu sync.Mutex
}

// NewBuildkiteScanner returns a Buildkite scanner.
func NewBuildkiteScanner(cfg BuildkiteConfig) ProviderScanner {
	return &buildkiteScanner{cfg: cfg}
}

func (s *buildkiteScanner) Provider() Provider { return ProviderBuildkite }

func (s *buildkiteScanner) ensureHTTP() error {
	s.httpMu.Lock()
	defer s.httpMu.Unlock()
	if s.http != nil {
		return nil
	}
	if strings.TrimSpace(s.cfg.BaseURL) == "" {
		return fmt.Errorf("buildkite scanner: BaseURL is required")
	}
	r, err := newHTTPReader(s.cfg.BaseURL, s.cfg.HTTPClient)
	if err != nil {
		return fmt.Errorf("buildkite scanner: %w", err)
	}
	s.http = r.withBearer(s.cfg.Token)
	return nil
}

func (s *buildkiteScanner) Scan(ctx context.Context, d *Detector) error {
	if err := s.ensureHTTP(); err != nil {
		return err
	}
	targets := s.cfg.Pipelines
	// When pipelines aren't explicitly listed, no list-pipelines call
	// is made — Buildkite's enumerate-org-pipelines endpoint can return
	// hundreds of inactive pipelines and inflates request budget. Force
	// operators to enumerate the pipelines they care about.
	if len(targets) == 0 {
		return nil
	}
	var firstErr error
	for _, slug := range targets {
		if err := s.scanPipeline(ctx, d, slug); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type buildkitePipeline struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Repository    string `json:"repository"`
	DefaultBranch string `json:"default_branch"`
	Configuration string `json:"configuration"`
}

type buildkiteBuild struct {
	ID      string `json:"id"`
	Number  int    `json:"number"`
	State   string `json:"state"`
	Branch  string `json:"branch"`
	Commit  string `json:"commit"`
	Source  string `json:"source"`
	Creator struct {
		Name string `json:"name"`
	} `json:"creator"`
	Jobs []struct {
		ID    string `json:"id"`
		Type  string `json:"type"`
		Agent struct {
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			MetaData []string `json:"meta_data"`
		} `json:"agent"`
		Env map[string]interface{} `json:"env"`
	} `json:"jobs"`
}

func (s *buildkiteScanner) scanPipeline(ctx context.Context, d *Detector, orgPipeline string) error {
	org, slug, ok := strings.Cut(orgPipeline, "/")
	if !ok || org == "" || slug == "" {
		return fmt.Errorf("buildkite scanner: invalid pipeline slug %q (want org/slug)", orgPipeline)
	}
	var pipeline buildkitePipeline
	rel := fmt.Sprintf("/v2/organizations/%s/pipelines/%s", org, slug)
	if status, _, err := s.http.get(ctx, rel, &pipeline); err != nil {
		return err
	} else if status == http.StatusNotFound {
		return nil
	}
	repoFull := buildkiteRepoFromConfig(pipeline.Repository)
	uses, runs := parseBuildkitePipelineYAML(pipeline.Configuration)

	var builds []buildkiteBuild
	buildsRel := fmt.Sprintf("/v2/organizations/%s/pipelines/%s/builds", org, slug)
	if _, _, err := s.http.get(ctx, buildsRel, &builds); err != nil {
		return err
	}
	if len(builds) > MaxBuildsPerScan {
		builds = builds[:MaxBuildsPerScan]
	}

	for _, b := range builds {
		envNames := []string{}
		labels := []string{}
		var runnerID, jobID string
		for _, j := range b.Jobs {
			if j.Agent.ID != "" && runnerID == "" {
				runnerID = j.Agent.ID
				labels = append(labels, j.Agent.Name)
				labels = append(labels, j.Agent.MetaData...)
			}
			if jobID == "" {
				jobID = j.ID
			}
			for k := range j.Env {
				if k != "" {
					envNames = append(envNames, k)
				}
			}
		}
		run := Run{
			Provider:     ProviderBuildkite,
			Workspace:    org,
			Repo:         repoFull,
			Ref:          b.Branch,
			HeadSHA:      b.Commit,
			RunID:        strconv.Itoa(b.Number),
			JobID:        jobID,
			WorkflowID:   slug,
			RunnerID:     runnerID,
			Event:        b.Source,
			Actor:        b.Creator.Name,
			Labels:       labels,
			EnvNames:     envNames,
			UsesActions:  uses,
			RunCommands:  runs,
			WorkflowPath: ".buildkite/pipeline.yml",
			WorkflowYAML: pipeline.Configuration,
			IsScheduled:  strings.EqualFold(b.Source, "schedule"),
		}
		if err := d.EmitRun(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

// buildkiteRepoFromConfig parses `<owner>/<repo>` out of a Buildkite
// repository field. Buildkite stores the literal `git@host:owner/repo.git`
// or `https://host/owner/repo.git` string the operator configured.
func buildkiteRepoFromConfig(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// SSH form: `git@host:owner/repo.git`
	if i := strings.Index(raw, ":"); i > 0 && strings.Contains(raw[:i], "@") {
		raw = raw[i+1:]
	}
	// HTTP form: strip scheme + host
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
		if j := strings.IndexByte(raw, '/'); j > 0 {
			raw = raw[j+1:]
		}
	}
	raw = strings.TrimSuffix(raw, ".git")
	if owner, name := parseOwnerRepo(raw); owner != "" && name != "" {
		return owner + "/" + name
	}
	return ""
}

// parseBuildkitePipelineYAML extracts `command:` leading tokens + plugin
// references from a Buildkite `pipeline.yml` body. The detector treats
// `plugins:` entries as `uses:` for action-match purposes.
func parseBuildkitePipelineYAML(yaml string) ([]string, []string) {
	if yaml == "" {
		return nil, nil
	}
	var uses, runs []string
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- command:") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- command:"))
			rest = strings.Trim(rest, "\"'")
			tok := firstShellToken(rest)
			if tok != "" {
				runs = append(runs, tok)
			}
		}
		if strings.HasPrefix(trimmed, "command:") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "command:"))
			rest = strings.Trim(rest, "\"'")
			tok := firstShellToken(rest)
			if tok != "" {
				runs = append(runs, tok)
			}
		}
		if strings.HasPrefix(trimmed, "- ") && strings.Contains(trimmed, "#") {
			// Plugin reference in short-form: `- org/plugin#tag`
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if i := strings.IndexByte(val, '#'); i > 0 {
				val = val[:i]
			}
			val = strings.TrimSpace(val)
			if val != "" {
				uses = append(uses, val)
			}
		}
	}
	return uses, runs
}
