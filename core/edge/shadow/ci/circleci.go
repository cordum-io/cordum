package ci

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// CircleCIConfig configures a CircleCI scanner. Read-only against the
// CircleCI v2 JSON API (`/api/v2/...`) plus the v1.1 configuration
// endpoint for the rendered `.circleci/config.yml` body.
type CircleCIConfig struct {
	// BaseURL is the CircleCI API root, e.g. `https://circleci.com`.
	BaseURL string
	// Token is a CircleCI personal API token. Sent as the
	// `Circle-Token` header per CircleCI v2 docs.
	Token string
	// Projects is the operator-supplied set of project slugs to scan,
	// each formatted as `<vcs>/<org>/<repo>` where vcs ∈ `gh|bb|...`.
	Projects []string
	// HTTPClient is the *http.Client to use. Nil falls back to a
	// timeout-bounded default.
	HTTPClient *http.Client
}

type circleciScanner struct {
	cfg    CircleCIConfig
	http   *httpReader
	httpMu sync.Mutex
}

// NewCircleCIScanner returns a CircleCI scanner.
func NewCircleCIScanner(cfg CircleCIConfig) ProviderScanner {
	return &circleciScanner{cfg: cfg}
}

func (s *circleciScanner) Provider() Provider { return ProviderCircleCI }

func (s *circleciScanner) ensureHTTP() error {
	s.httpMu.Lock()
	defer s.httpMu.Unlock()
	if s.http != nil {
		return nil
	}
	if strings.TrimSpace(s.cfg.BaseURL) == "" {
		return fmt.Errorf("circleci scanner: BaseURL is required")
	}
	r, err := newHTTPReader(s.cfg.BaseURL, s.cfg.HTTPClient)
	if err != nil {
		return fmt.Errorf("circleci scanner: %w", err)
	}
	s.http = r.withTokenHeader("Circle-Token", s.cfg.Token)
	return nil
}

func (s *circleciScanner) Scan(ctx context.Context, d *Detector) error {
	if len(s.cfg.Projects) == 0 {
		return nil
	}
	if err := s.ensureHTTP(); err != nil {
		return err
	}
	var firstErr error
	for _, slug := range s.cfg.Projects {
		if err := s.scanProject(ctx, d, slug); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type circleciProject struct {
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	OrganizationName string `json:"organization_name"`
	VCSInfo          struct {
		VCSURL        string `json:"vcs_url"`
		DefaultBranch string `json:"default_branch"`
	} `json:"vcs_info"`
}

type circleciPipelineResp struct {
	Items []struct {
		ID     string `json:"id"`
		Number int    `json:"number"`
		State  string `json:"state"`
		VCS    struct {
			Branch   string `json:"branch"`
			Revision string `json:"revision"`
		} `json:"vcs"`
		Trigger struct {
			Type  string `json:"type"`
			Actor struct {
				Login string `json:"login"`
			} `json:"actor"`
		} `json:"trigger"`
	} `json:"items"`
}

type circleciWorkflowResp struct {
	Items []struct {
		ID         string `json:"id"`
		PipelineID string `json:"pipeline_id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
	} `json:"items"`
}

type circleciJobResp struct {
	Items []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Status    string `json:"status"`
		Type      string `json:"type"`
		JobNumber int    `json:"job_number"`
	} `json:"items"`
}

type circleciEnvvarResp struct {
	Items []struct {
		Name string `json:"name"`
	} `json:"items"`
}

func (s *circleciScanner) scanProject(ctx context.Context, d *Detector, slug string) error {
	var project circleciProject
	if status, _, err := s.http.get(ctx, "/api/v2/project/"+slug, &project); err != nil {
		return err
	} else if status == http.StatusNotFound {
		return nil
	}
	repoFull := circleciRepoFromVCS(project.VCSInfo.VCSURL, project.OrganizationName, project.Name)
	envNames := s.fetchEnvVarNames(ctx, slug)
	configYAML := s.fetchConfigYAML(ctx, slug)
	uses, runs := parseCircleCIConfigYAML(configYAML)

	var pipelines circleciPipelineResp
	if _, _, err := s.http.get(ctx, "/api/v2/project/"+slug+"/pipeline", &pipelines); err != nil {
		return err
	}
	if len(pipelines.Items) > MaxRunsPerScan {
		pipelines.Items = pipelines.Items[:MaxRunsPerScan]
	}
	for _, p := range pipelines.Items {
		var workflows circleciWorkflowResp
		_, _, _ = s.http.get(ctx, "/api/v2/pipeline/"+p.ID+"/workflow", &workflows)
		var jobID, workflowID string
		for _, w := range workflows.Items {
			if workflowID == "" {
				workflowID = w.ID
			}
			var jobs circleciJobResp
			_, _, _ = s.http.get(ctx, "/api/v2/workflow/"+w.ID+"/job", &jobs)
			for _, j := range jobs.Items {
				if jobID == "" {
					jobID = j.ID
				}
			}
		}
		run := Run{
			Provider:     ProviderCircleCI,
			Workspace:    project.OrganizationName,
			Repo:         repoFull,
			Ref:          p.VCS.Branch,
			HeadSHA:      p.VCS.Revision,
			RunID:        strconv.Itoa(p.Number),
			JobID:        jobID,
			WorkflowID:   workflowID,
			Event:        p.Trigger.Type,
			Actor:        p.Trigger.Actor.Login,
			EnvNames:     envNames,
			UsesActions:  uses,
			RunCommands:  runs,
			WorkflowPath: ".circleci/config.yml",
			WorkflowYAML: configYAML,
			IsScheduled:  strings.EqualFold(p.Trigger.Type, "schedule") || strings.EqualFold(p.Trigger.Type, "scheduled_pipeline"),
		}
		if err := d.EmitRun(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (s *circleciScanner) fetchEnvVarNames(ctx context.Context, slug string) []string {
	var resp circleciEnvvarResp
	if status, _, err := s.http.get(ctx, "/api/v2/project/"+slug+"/envvar", &resp); err != nil || status == http.StatusNotFound {
		return nil
	}
	out := make([]string, 0, len(resp.Items))
	for _, e := range resp.Items {
		if e.Name != "" {
			out = append(out, e.Name)
		}
	}
	return out
}

func (s *circleciScanner) fetchConfigYAML(ctx context.Context, slug string) string {
	// slug is `gh/org/repo`; the v1.1 endpoint uses `github/org/repo` —
	// translate VCS prefix.
	vcsSlug := translateCircleCISlug(slug)
	if vcsSlug == "" {
		return ""
	}
	rel := "/api/v1.1/project/" + vcsSlug + "/configuration"
	status, body, err := s.http.getRaw(ctx, rel)
	if err != nil || status == http.StatusNotFound {
		return ""
	}
	return string(body)
}

func translateCircleCISlug(slug string) string {
	parts := strings.SplitN(slug, "/", 3)
	if len(parts) != 3 {
		return ""
	}
	switch strings.ToLower(parts[0]) {
	case "gh", "github":
		return "github/" + parts[1] + "/" + parts[2]
	case "bb", "bitbucket":
		return "bitbucket/" + parts[1] + "/" + parts[2]
	default:
		return strings.ToLower(parts[0]) + "/" + parts[1] + "/" + parts[2]
	}
}

func circleciRepoFromVCS(vcsURL, org, name string) string {
	if vcsURL != "" {
		raw := vcsURL
		if i := strings.Index(raw, "://"); i >= 0 {
			raw = raw[i+3:]
		}
		if i := strings.IndexByte(raw, '/'); i > 0 {
			raw = raw[i+1:]
		}
		raw = strings.TrimSuffix(raw, ".git")
		if owner, repo := parseOwnerRepo(raw); owner != "" && repo != "" {
			return owner + "/" + repo
		}
	}
	if org != "" && name != "" {
		return org + "/" + name
	}
	return ""
}

// parseCircleCIConfigYAML extracts `image:` references and `run:`
// leading shell tokens from a `.circleci/config.yml` body. Tolerant
// of malformed YAML.
func parseCircleCIConfigYAML(yaml string) ([]string, []string) {
	if yaml == "" {
		return nil, nil
	}
	var uses, runs []string
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- image:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "- image:"))
			val = strings.Trim(val, "\"'")
			if val != "" {
				uses = append(uses, val)
			}
		} else if strings.HasPrefix(trimmed, "image:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))
			val = strings.Trim(val, "\"'")
			if val != "" {
				uses = append(uses, val)
			}
		}
		if strings.HasPrefix(trimmed, "- run:") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- run:"))
			rest = strings.Trim(rest, "\"'")
			tok := firstShellToken(rest)
			if tok != "" {
				runs = append(runs, tok)
			}
		}
		if strings.HasPrefix(trimmed, "run:") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "run:"))
			rest = strings.Trim(rest, "\"'")
			tok := firstShellToken(rest)
			if tok != "" {
				runs = append(runs, tok)
			}
		}
	}
	return uses, runs
}
