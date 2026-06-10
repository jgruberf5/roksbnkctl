package test

import (
	"strings"
	"testing"
)

const matrixSample = `
gateway:
  class_name: bnk-gateway-class
  app_namespace: bnk-apps
  name: bnk-gateway
  http_section: http
  https_section: https
  tcp_section: tcp
fixtures:
  iperf3_server: true
  http_backend: true
  routes: true
endpoints:
  vsi-same-zone: { kind: vsi, target: jumphost-jp-osa-1 }
  vsi-diff-vpc:  { kind: vsi, target: jumphost }
  tmm-tcp:       { kind: address, host: 10.0.0.5, port: 5201 }
  tmm-http:      { kind: url, url: "http://10.0.0.5/128" }
  tmm-https:     { kind: url, url: "https://10.0.0.5/512k" }
cells:
  - name: "L4 iperf3 128B VSI->TMM same-zone"
    family: iperf3
    client: vsi-same-zone
    server: tmm-tcp
    length: "128"
    duration: 30
  - name: "L4 iperf3 512K VSI->TMM same-zone"
    family: iperf3
    client: vsi-same-zone
    server: tmm-tcp
    length: "512K"
    duration: 30
  - name: "L7 http CPS VSI->TMM diff-vpc"
    family: l7
    client: vsi-diff-vpc
    server: tmm-http
    l7: { mode: cps }
  - name: "L7 https TPS VSI->TMM diff-vpc"
    family: l7
    client: vsi-diff-vpc
    server: tmm-https
    l7: { mode: tps, duration: 30 }
`

func TestParseMatrix(t *testing.T) {
	spec, err := ParseMatrix([]byte(matrixSample))
	if err != nil {
		t.Fatalf("ParseMatrix: %v", err)
	}
	if len(spec.Cells) != 4 {
		t.Fatalf("cells = %d, want 4", len(spec.Cells))
	}
	if !spec.Fixtures.Routes || spec.Gateway.ClassName != "bnk-gateway-class" {
		t.Errorf("gateway/fixtures not parsed: %+v / %+v", spec.Gateway, spec.Fixtures)
	}
}

func TestExpand_ResolvesBackendsAndArgv(t *testing.T) {
	spec, err := ParseMatrix([]byte(matrixSample))
	if err != nil {
		t.Fatalf("ParseMatrix: %v", err)
	}
	cells, err := spec.Expand("")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(cells) != 4 {
		t.Fatalf("expanded %d cells, want 4", len(cells))
	}

	// iperf3 cell → ssh backend, -l 128, port 5201.
	c0 := cells[0]
	if c0.BackendSpec != "ssh:jumphost-jp-osa-1" {
		t.Errorf("c0 backend = %q", c0.BackendSpec)
	}
	argv0, _ := c0.Argv()
	got := strings.Join(argv0, " ")
	for _, want := range []string{"-c 10.0.0.5", "-p 5201", "-l 128", "-t 30", "-J"} {
		if !strings.Contains(got, want) {
			t.Errorf("iperf3 argv %q missing %q", got, want)
		}
	}
	if c0.Tool() != "iperf3" {
		t.Errorf("c0 tool = %q", c0.Tool())
	}

	// l7 https cell → h2load, https URL, duration mode.
	c3 := cells[3]
	if c3.Tool() != "h2load" {
		t.Errorf("c3 tool = %q", c3.Tool())
	}
	argv3, err := c3.Argv()
	if err != nil {
		t.Fatalf("c3 argv: %v", err)
	}
	got3 := strings.Join(argv3, " ")
	if !strings.Contains(got3, "https://10.0.0.5/512k") {
		t.Errorf("h2load argv %q missing https url", got3)
	}
	if !strings.Contains(got3, "-D 30") {
		t.Errorf("h2load argv %q missing -D 30", got3)
	}
}

func TestExpand_OnlyGlob(t *testing.T) {
	spec, _ := ParseMatrix([]byte(matrixSample))
	cells, err := spec.Expand("L7*")
	if err != nil {
		t.Fatalf("Expand only: %v", err)
	}
	if len(cells) != 2 {
		t.Fatalf("only L7* → %d cells, want 2", len(cells))
	}
	if _, err := spec.Expand("nope*"); err == nil {
		t.Error("expected error when glob matches nothing")
	}
}

func TestValidate_Errors(t *testing.T) {
	cases := map[string]string{
		"undefined client endpoint": `
endpoints: { s: { kind: address, host: h } }
cells: [ { name: x, family: iperf3, client: ghost, server: s } ]`,
		"iperf3 server must be address": `
endpoints: { u: { kind: url, url: "http://x" } }
cells: [ { name: x, family: iperf3, server: u } ]`,
		"l7 needs mode": `
endpoints: { u: { kind: url, url: "http://x" } }
cells: [ { name: x, family: l7, server: u } ]`,
		"routes need gateway identity": `
fixtures: { routes: true }
endpoints: { s: { kind: address, host: h } }
cells: [ { name: x, family: iperf3, server: s } ]`,
		"unknown family": `
endpoints: { s: { kind: address, host: h } }
cells: [ { name: x, family: bogus, server: s } ]`,
	}
	for name, y := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseMatrix([]byte(y)); err == nil {
				t.Errorf("expected validation error for %q", name)
			}
		})
	}
}

func TestPlanString(t *testing.T) {
	spec, _ := ParseMatrix([]byte(matrixSample))
	cells, _ := spec.Expand("")
	plan := PlanString(cells)
	if !strings.Contains(plan, "iperf3 -c 10.0.0.5") {
		t.Errorf("plan missing iperf3 run line:\n%s", plan)
	}
	if !strings.Contains(plan, "h2load -c") {
		t.Errorf("plan missing h2load run line:\n%s", plan)
	}
	if !strings.Contains(plan, "ssh:jumphost") {
		t.Errorf("plan missing ssh backend:\n%s", plan)
	}
}
