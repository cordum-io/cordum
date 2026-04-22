package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// The cordum:// URI scheme (task-466b6a6a).
//
// MCP clients dereference these URIs to pull live platform state. The
// scheme is intentionally small and stable — it is part of Cordum's
// public API surface (see docs/mcp/resources.md for the stability
// policy). Templates registered here cover the seven core record
// types: jobs, runs, run timelines, workflows, packs, topics, agents.
// Audit chain access is admin-only and lives under cordum://audit/.
//
// URI grammar (human-readable — the parser below is strict):
//   cordum://jobs/<job_id>
//   cordum://runs/<run_id>
//   cordum://runs/<run_id>/timeline
//   cordum://workflows/<workflow_id>
//   cordum://packs/<pack_id>
//   cordum://topics/<topic_name>
//   cordum://agents/<agent_id>
//   cordum://audit/<tenant>/<seq>
//
// Every handler pulls through the same ServiceBridge wired for the
// tool surface; operators don't have to configure two auth paths.

const cordumURIScheme = "cordum"

// ErrInvalidCordumURI is returned by parseCordumURI when the URI does
// not match any registered template.
var ErrInvalidCordumURI = errors.New("mcp: invalid cordum:// URI")

// cordumURI is the parsed shape of a cordum:// reference. Kind is the
// first path segment (jobs, runs, workflows, ...); ID is the second
// segment; SubResource is the optional third (only "timeline" today,
// reserved for future use). For audit URIs, ID is the tenant and Extra
// is the sequence number.
type cordumURI struct {
	Kind        string
	ID          string
	SubResource string
	Extra       string
}

// parseCordumURI accepts a cordum:// URI and returns its parsed
// components. Rejects any other scheme or a malformed path.
func parseCordumURI(raw string) (cordumURI, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return cordumURI{}, fmt.Errorf("%w: %v", ErrInvalidCordumURI, err)
	}
	if u.Scheme != cordumURIScheme {
		return cordumURI{}, fmt.Errorf("%w: scheme=%q want %q", ErrInvalidCordumURI, u.Scheme, cordumURIScheme)
	}
	kind := strings.TrimSpace(u.Host)
	if kind == "" {
		return cordumURI{}, fmt.Errorf("%w: missing kind segment", ErrInvalidCordumURI)
	}
	path := strings.Trim(u.Path, "/")
	parts := []string{}
	if path != "" {
		parts = strings.Split(path, "/")
	}
	switch len(parts) {
	case 0:
		return cordumURI{}, fmt.Errorf("%w: missing id segment", ErrInvalidCordumURI)
	case 1:
		return cordumURI{Kind: kind, ID: parts[0]}, nil
	case 2:
		// Two forms: runs/<id>/timeline (sub-resource) OR audit/<tenant>/<seq>.
		if kind == "audit" {
			return cordumURI{Kind: kind, ID: parts[0], Extra: parts[1]}, nil
		}
		return cordumURI{Kind: kind, ID: parts[0], SubResource: parts[1]}, nil
	default:
		return cordumURI{}, fmt.Errorf("%w: too many path segments", ErrInvalidCordumURI)
	}
}

// RegisterCordumURIResources registers the cordum:// URI templates on
// the given resource registry. Called from the gateway's MCP startup
// path so clients listing resources see the full scheme immediately.
func RegisterCordumURIResources(registry *ResourceRegistry, bridge ServiceBridge) error {
	if registry == nil {
		return fmt.Errorf("resource registry is nil")
	}
	if bridge == nil {
		return fmt.Errorf("service bridge is nil")
	}
	templates := []struct {
		tmpl    ResourceTemplate
		handler ResourceHandler
	}{
		{
			tmpl: ResourceTemplate{
				URITemplate: "cordum://jobs/{id}",
				Name:        "Cordum job",
				Description: "Inspect a job by id. Dereference returns the full job record (prompt, topic, state, decision).",
				MIMEType:    "application/json",
			},
			handler: cordumResourceHandler(bridge, "jobs"),
		},
		{
			tmpl: ResourceTemplate{
				URITemplate: "cordum://runs/{id}",
				Name:        "Cordum workflow run",
				Description: "Inspect a workflow run by id. Dereference returns graph state, pending steps, outputs.",
				MIMEType:    "application/json",
			},
			handler: cordumResourceHandler(bridge, "runs"),
		},
		{
			tmpl: ResourceTemplate{
				URITemplate: "cordum://runs/{id}/timeline",
				Name:        "Cordum run timeline",
				Description: "Ordered timeline of state transitions and step events for a workflow run.",
				MIMEType:    "application/json",
			},
			handler: cordumResourceHandler(bridge, "runs"),
		},
		{
			tmpl: ResourceTemplate{
				URITemplate: "cordum://workflows/{id}",
				Name:        "Cordum workflow",
				Description: "Workflow definition by id: title, version, step graph.",
				MIMEType:    "application/json",
			},
			handler: cordumResourceHandler(bridge, "workflows"),
		},
		{
			tmpl: ResourceTemplate{
				URITemplate: "cordum://packs/{id}",
				Name:        "Cordum pack",
				Description: "Installed integration pack by id: name, version, enabled state, install timestamp.",
				MIMEType:    "application/json",
			},
			handler: cordumResourceHandler(bridge, "packs"),
		},
		{
			tmpl: ResourceTemplate{
				URITemplate: "cordum://topics/{name}",
				Name:        "Cordum topic",
				Description: "Job topic by name: allowed publishers, pool binding, schema refs.",
				MIMEType:    "application/json",
			},
			handler: cordumResourceHandler(bridge, "topics"),
		},
		{
			tmpl: ResourceTemplate{
				URITemplate: "cordum://agents/{id}",
				Name:        "Cordum agent identity",
				Description: "Agent identity record: allowed tools, risk tier, data classifications, status.",
				MIMEType:    "application/json",
			},
			handler: cordumResourceHandler(bridge, "agents"),
		},
		{
			tmpl: ResourceTemplate{
				URITemplate: "cordum://audit/{tenant}/{seq}",
				Name:        "Cordum audit event",
				Description: "Single audit-chain event for a tenant, indexed by sequence number. Admin-only.",
				MIMEType:    "application/json",
			},
			handler: cordumResourceHandler(bridge, "audit"),
		},
	}
	for _, t := range templates {
		if err := registry.RegisterTemplate(t.tmpl, t.handler); err != nil {
			return err
		}
	}
	return nil
}

// cordumResourceHandler returns a ResourceHandler that parses the URI,
// matches the kind, calls the matching ServiceBridge method, and
// returns the response body as a JSON text content item.
func cordumResourceHandler(bridge ServiceBridge, expectedKind string) ResourceHandler {
	return func(ctx context.Context, uri string) (*ResourceContents, error) {
		parsed, err := parseCordumURI(uri)
		if err != nil {
			return nil, err
		}
		if parsed.Kind != expectedKind {
			return nil, fmt.Errorf("%w: kind %q does not match template (expected %s)", ErrInvalidCordumURI, parsed.Kind, expectedKind)
		}
		var (
			item *ResourceItem
			bErr error
		)
		switch parsed.Kind {
		case "jobs":
			item, bErr = bridge.GetJob(ctx, parsed.ID)
		case "runs":
			if parsed.SubResource == "timeline" {
				item, bErr = bridge.GetRunTimeline(ctx, parsed.ID)
			} else {
				item, bErr = bridge.GetRun(ctx, parsed.ID)
			}
		case "workflows":
			// Workflows don't yet have a dedicated GetByID; fall back to a
			// filtered list. Once the gateway exposes /workflows/{id} we
			// wire a real GetWorkflow method.
			page, err := bridge.ListWorkflows(ctx, ListInput{Filter: map[string]string{"id": parsed.ID}, PageSize: 1})
			if err != nil {
				return nil, mapBridgeError(err)
			}
			if len(page.Items) == 0 {
				return nil, fmt.Errorf("workflow %s not found", parsed.ID)
			}
			item = &ResourceItem{ID: parsed.ID, Kind: "workflow", Data: page.Items[0]}
		case "packs":
			page, err := bridge.ListPacks(ctx, ListInput{Filter: map[string]string{"id": parsed.ID}, PageSize: 1})
			if err != nil {
				return nil, mapBridgeError(err)
			}
			if len(page.Items) == 0 {
				return nil, fmt.Errorf("pack %s not found", parsed.ID)
			}
			item = &ResourceItem{ID: parsed.ID, Kind: "pack", Data: page.Items[0]}
		case "topics":
			page, err := bridge.ListTopics(ctx, ListInput{Filter: map[string]string{"name": parsed.ID}, PageSize: 1})
			if err != nil {
				return nil, mapBridgeError(err)
			}
			if len(page.Items) == 0 {
				return nil, fmt.Errorf("topic %s not found", parsed.ID)
			}
			item = &ResourceItem{ID: parsed.ID, Kind: "topic", Data: page.Items[0]}
		case "agents":
			page, err := bridge.ListAgents(ctx, ListInput{Filter: map[string]string{"id": parsed.ID}, PageSize: 1})
			if err != nil {
				return nil, mapBridgeError(err)
			}
			if len(page.Items) == 0 {
				return nil, fmt.Errorf("agent %s not found", parsed.ID)
			}
			item = &ResourceItem{ID: parsed.ID, Kind: "agent", Data: page.Items[0]}
		case "audit":
			// Audit URIs use tenant + seq; filter on seq and limit 1.
			page, err := bridge.QueryAudit(ctx, AuditQueryInput{
				ListInput: ListInput{Filter: map[string]string{"seq": parsed.Extra}, PageSize: 1, Tenant: parsed.ID},
				Tenant:    parsed.ID,
			})
			if err != nil {
				return nil, mapBridgeError(err)
			}
			if len(page.Items) == 0 {
				return nil, fmt.Errorf("audit event seq=%s not found", parsed.Extra)
			}
			item = &ResourceItem{ID: parsed.ID + "/" + parsed.Extra, Kind: "audit_event", Data: page.Items[0]}
		default:
			return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalidCordumURI, parsed.Kind)
		}
		if bErr != nil {
			return nil, mapBridgeError(bErr)
		}
		data, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("encode resource: %w", err)
		}
		return &ResourceContents{URI: uri, MIMEType: "application/json", Text: string(data)}, nil
	}
}
