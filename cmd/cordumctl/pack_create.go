package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var packIDPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

func runPackCreate(args []string) {
	fs := flag.NewFlagSet("pack create", flag.ExitOnError)
	dir := fs.String("dir", "", "output directory (defaults to pack id)")
	force := fs.Bool("force", false, "overwrite existing files")
	if err := fs.Parse(args); err != nil {
		fail(err.Error())
	}
	if fs.NArg() < 1 {
		fail("pack id required")
	}
	packID := fs.Arg(0)
	if !packIDPattern.MatchString(packID) {
		fail(fmt.Sprintf("pack id must match %s", packIDPattern.String()))
	}
	target := *dir
	if target == "" {
		target = packID
	}
	if err := scaffoldPack(target, packID, *force); err != nil {
		fail(err.Error())
	}
	fmt.Printf("Pack scaffold created at %s\n", target)
}

func scaffoldPack(target, packID string, force bool) error {
	info, err := os.Stat(target)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("not a directory: %s", target)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := ensureDir(target); err != nil {
		return err
	}

	files := map[string]string{
		filepath.Join(target, "pack.yaml"):                           packManifestTemplate(packID),
		filepath.Join(target, "README.md"):                           packReadmeTemplate(packID),
		filepath.Join(target, "schemas", "EchoInput.json"):           packSchemaTemplate(packID),
		filepath.Join(target, "workflows", "echo.yaml"):              packWorkflowTemplate(packID),
		filepath.Join(target, "overlays", "pools.patch.yaml"):        packPoolsTemplate(packID),
		filepath.Join(target, "overlays", "timeouts.patch.yaml"):     packTimeoutsTemplate(packID),
		filepath.Join(target, "overlays", "policy.fragment.yaml"):    packPolicyTemplate(packID),
	}
	for path, content := range files {
		if err := writeFile(path, content, force); err != nil {
			return err
		}
	}
	return nil
}

func packManifestTemplate(packID string) string {
	return fmt.Sprintf(`apiVersion: cordum.io/v1alpha1
kind: Pack

metadata:
  id: %s
  version: 0.1.0
  title: %s Pack
  description: %s workflow pack.

compatibility:
  protocolVersion: 1
  minCoreVersion: 0.6.0

topics:
  - name: job.%s.echo
    capability: %s.echo

resources:
  schemas:
    - id: %s/EchoInput
      path: schemas/EchoInput.json
  workflows:
    - id: %s.echo
      path: workflows/echo.yaml

overlays:
  config:
    - name: pools
      scope: system
      key: pools
      strategy: json_merge_patch
      path: overlays/pools.patch.yaml
    - name: timeouts
      scope: system
      key: timeouts
      strategy: json_merge_patch
      path: overlays/timeouts.patch.yaml
  policy:
    - name: safety
      strategy: bundle_fragment
      path: overlays/policy.fragment.yaml

tests:
  policySimulations:
    - name: allow_echo
      request:
        tenantId: default
        topic: job.%s.echo
        capability: %s.echo
      expectDecision: ALLOW
`, packID, packID, packID, packID, packID, packID, packID, packID, packID)
}

func packWorkflowTemplate(packID string) string {
	return fmt.Sprintf(`id: %s.echo
name: %s Echo
org_id: default
steps:
  echo:
    id: echo
    name: Echo input
    type: worker
    topic: job.%s.echo
    input_schema_id: %s/EchoInput
    input:
      message: "${input.message}"
      author: "${input.author}"
    meta:
      pack_id: %s
      capability: %s.echo
`, packID, packID, packID, packID, packID, packID)
}

func packSchemaTemplate(packID string) string {
	return fmt.Sprintf(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "%s Echo Input",
  "type": "object",
  "properties": {
    "message": {
      "type": "string"
    },
    "author": {
      "type": "string"
    }
  },
  "required": ["message"],
  "additionalProperties": false
}
`, packID)
}

func packPoolsTemplate(packID string) string {
	return fmt.Sprintf(`topics:
  job.%s.echo: %s
pools:
  %s:
    requires:
      - local
`, packID, packID, packID)
}

func packTimeoutsTemplate(packID string) string {
	return fmt.Sprintf(`workflows:
  %s.echo: 120
`, packID)
}

func packPolicyTemplate(packID string) string {
	return fmt.Sprintf(`rules:
  - id: %s-allow
    match:
      topics:
        - job.%s.echo
    decision: allow
    reason: "Allow the %s echo demo."
`, packID, packID, packID)
}

func packReadmeTemplate(packID string) string {
	return fmt.Sprintf(`# %s Pack

Generated pack scaffold.

## Install

~~~bash
cordumctl pack install ./%s
~~~

## Run

~~~bash
curl -sS -X POST http://localhost:8081/api/v1/workflows/%s.echo/runs \
  -H "X-API-Key: ${CORDUM_API_KEY:-super-secret-key}" \
  -H "Content-Type: application/json" \
  -d '{"message":"hello from %s"}' | jq
~~~
`, packID, packID, packID, packID)
}
