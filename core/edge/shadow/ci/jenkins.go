package ci

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// JenkinsConfig configures a Jenkins scanner. Per design §8.3 the
// scanner READS only — `/job/<name>/api/json?depth=1`, the per-build
// JSON, and `config.xml` for the Jenkinsfile script. It never POSTs,
// never creates a crumb, and never fetches console output.
type JenkinsConfig struct {
	// BaseURL is the operator-supplied Jenkins root URL. The scanner
	// refuses to start unless this is non-empty.
	BaseURL string
	// Username + APIToken populate HTTP Basic auth. Empty == anonymous
	// (Jenkins instances with security disabled).
	Username string
	APIToken string
	// Jobs is the operator-supplied set of job names (or folder paths
	// like `myfolder/job/innerjob`) to scan. Empty == nothing scanned.
	Jobs []string
	// HTTPClient is the *http.Client to use. Nil falls back to a
	// timeout-bounded default.
	HTTPClient *http.Client
}

type jenkinsScanner struct {
	cfg    JenkinsConfig
	http   *httpReader
	httpMu sync.Mutex
}

// NewJenkinsScanner returns a Jenkins scanner.
func NewJenkinsScanner(cfg JenkinsConfig) ProviderScanner {
	return &jenkinsScanner{cfg: cfg}
}

func (s *jenkinsScanner) Provider() Provider { return ProviderJenkins }

func (s *jenkinsScanner) ensureHTTP() error {
	s.httpMu.Lock()
	defer s.httpMu.Unlock()
	if s.http != nil {
		return nil
	}
	if strings.TrimSpace(s.cfg.BaseURL) == "" {
		return fmt.Errorf("jenkins scanner: BaseURL is required")
	}
	r, err := newHTTPReader(s.cfg.BaseURL, s.cfg.HTTPClient)
	if err != nil {
		return fmt.Errorf("jenkins scanner: %w", err)
	}
	s.http = r.withBasicAuth(s.cfg.Username, s.cfg.APIToken)
	return nil
}

func (s *jenkinsScanner) Scan(ctx context.Context, d *Detector) error {
	if len(s.cfg.Jobs) == 0 {
		return nil
	}
	if err := s.ensureHTTP(); err != nil {
		return err
	}
	var firstErr error
	for _, j := range s.cfg.Jobs {
		if err := s.scanJob(ctx, d, j); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type jenkinsJob struct {
	Name      string `json:"name"`
	FullName  string `json:"fullName"`
	URL       string `json:"url"`
	LastBuild struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	} `json:"lastBuild"`
	SCM struct {
		UserRemoteConfigs []struct {
			URL string `json:"url"`
		} `json:"userRemoteConfigs"`
	} `json:"scm"`
}

type jenkinsBuild struct {
	Number          int    `json:"number"`
	Result          string `json:"result"`
	FullDisplayName string `json:"fullDisplayName"`
	BuiltOn         string `json:"builtOn"`
	Actions         []struct {
		Causes []struct {
			UserID   string `json:"userId"`
			UserName string `json:"userName"`
		} `json:"causes"`
	} `json:"actions"`
	Environment map[string]interface{} `json:"environment"`
}

type jenkinsConfigXML struct {
	XMLName    xml.Name `xml:"flow-definition"`
	Definition struct {
		Script string `xml:"script"`
	} `xml:"definition"`
}

func (s *jenkinsScanner) scanJob(ctx context.Context, d *Detector, jobPath string) error {
	jobAPI := jenkinsAPIPath(jobPath) + "/api/json"
	var jj jenkinsJob
	status, _, err := s.http.get(ctx, jobAPI, &jj)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound || strings.TrimSpace(jj.Name) == "" {
		return nil
	}
	var buildJSON jenkinsBuild
	if jj.LastBuild.Number > 0 {
		buildAPI := jenkinsAPIPath(jobPath) + "/" + strconv.Itoa(jj.LastBuild.Number) + "/api/json"
		_, _, _ = s.http.get(ctx, buildAPI, &buildJSON)
	}

	configXML := s.fetchJobConfig(ctx, jobPath)
	uses, runs := parseJenkinsfile(configXML)
	envNames := jenkinsEnvKeyNames(buildJSON.Environment)
	repoFull := jenkinsRepoFromSCM(jj)
	actor := jenkinsActor(buildJSON)

	run := Run{
		Provider:     ProviderJenkins,
		Workspace:    "",
		Repo:         repoFull,
		Ref:          "",
		RunID:        strconv.Itoa(buildJSON.Number),
		JobID:        strconv.Itoa(buildJSON.Number),
		WorkflowID:   jj.FullName,
		RunnerID:     buildJSON.BuiltOn,
		Event:        "build",
		Actor:        actor,
		Labels:       []string{buildJSON.BuiltOn},
		EnvNames:     envNames,
		UsesActions:  uses,
		RunCommands:  runs,
		WorkflowPath: "Jenkinsfile",
		WorkflowYAML: configXML,
	}
	return d.EmitRun(ctx, run)
}

func (s *jenkinsScanner) fetchJobConfig(ctx context.Context, jobPath string) string {
	rel := jenkinsAPIPath(jobPath) + "/config.xml"
	status, body, err := s.http.getRaw(ctx, rel)
	if err != nil || status == http.StatusNotFound {
		return ""
	}
	return string(body)
}

// jenkinsAPIPath translates `myfolder/innerjob` into `/job/myfolder/job/innerjob`.
func jenkinsAPIPath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		b.WriteString("/job/")
		b.WriteString(part)
	}
	return b.String()
}

// parseJenkinsfile extracts agent action/image refs and leading shell
// tokens from a Declarative or Scripted Jenkinsfile embedded in a
// config.xml. The parser tolerates malformed XML and degraded inputs.
func parseJenkinsfile(configXML string) ([]string, []string) {
	if configXML == "" {
		return nil, nil
	}
	var cfg jenkinsConfigXML
	if err := xml.Unmarshal([]byte(configXML), &cfg); err == nil && cfg.Definition.Script != "" {
		return parseJenkinsfileScript(cfg.Definition.Script)
	}
	// Inline Jenkinsfile body without `<script>` wrapper — fall back to
	// scanning the whole config XML as text.
	return parseJenkinsfileScript(configXML)
}

func parseJenkinsfileScript(script string) ([]string, []string) {
	var uses, runs []string
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "image ") || strings.HasPrefix(trimmed, "image:") || strings.HasPrefix(trimmed, "agent ") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "image"))
			rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
			rest = strings.TrimSpace(strings.TrimPrefix(rest, " "))
			val := strings.Trim(rest, "\"'{}")
			if val != "" && !strings.EqualFold(val, "any") {
				uses = append(uses, val)
			}
		}
		if strings.HasPrefix(trimmed, "sh ") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "sh "))
			rest = strings.Trim(rest, "\"'")
			tok := firstShellToken(rest)
			if tok != "" {
				runs = append(runs, tok)
			}
		}
	}
	return uses, runs
}

// jenkinsEnvKeyNames pulls NAMES out of a Jenkins build's `environment`
// blob. Values are dropped — by contract the detector never persists
// env values.
func jenkinsEnvKeyNames(env map[string]interface{}) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k := range env {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// jenkinsRepoFromSCM parses `<owner>/<repo>` out of a SCM userRemoteConfigs
// entry. Tolerates http(s):// and git@ shapes; returns empty when no
// recognisable repo path is present.
func jenkinsRepoFromSCM(jj jenkinsJob) string {
	for _, cfg := range jj.SCM.UserRemoteConfigs {
		url := cfg.URL
		if url == "" {
			continue
		}
		// Strip transport prefix.
		if i := strings.Index(url, "@"); i > 0 {
			url = url[i+1:]
		}
		if i := strings.Index(url, "://"); i >= 0 {
			url = url[i+3:]
		}
		// host/owner/repo[.git]
		if i := strings.IndexByte(url, '/'); i > 0 {
			url = url[i+1:]
		}
		url = strings.TrimSuffix(url, ".git")
		if owner, name := parseOwnerRepo(url); owner != "" && name != "" {
			return owner + "/" + name
		}
	}
	return ""
}

func jenkinsActor(b jenkinsBuild) string {
	for _, a := range b.Actions {
		for _, c := range a.Causes {
			if c.UserID != "" {
				return c.UserID
			}
			if c.UserName != "" {
				return c.UserName
			}
		}
	}
	return ""
}
