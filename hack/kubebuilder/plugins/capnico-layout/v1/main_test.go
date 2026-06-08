package main

import (
	"strings"
	"testing"
)

func TestUnit_AdaptCreateAPI(t *testing.T) {
	type parameters struct {
		args         []string
		universe     map[string]string
		wantContents map[string]string
		wantRemoved  []string
		wantError    bool
	}

	tests := map[string]parameters{
		"moves controller to top-level controllers package": {
			args: []string{"--group infrastructure", "--version v1alpha1", "--kind NicoCluster", "--resource=true", "--controller=true"},
			universe: map[string]string{
				"internal/controller/nicocluster_controller.go":      "package controller\n\ntype NicoClusterReconciler struct{}\n",
				"internal/controller/nicocluster_controller_test.go": "package controller\n",
				"internal/controller/suite_test.go":                  "package controller\n",
				"cmd/main.go":                                        "\"example.com/capnico/internal/controller\"\ncontroller.NicoClusterReconciler{}\n",
			},
			wantContents: map[string]string{
				"controllers/nicocluster_controller.go":      "package controllers\n\ntype NicoClusterReconciler struct{}\n",
				"controllers/nicocluster_controller_test.go": "package controllers\n",
				"controllers/suite_test.go":                  "package controllers\n",
			},
			wantRemoved: []string{
				"internal/controller/nicocluster_controller.go",
				"internal/controller/nicocluster_controller_test.go",
				"internal/controller/suite_test.go",
			},
		},
		"skips controller move when controller generation is disabled": {
			args: []string{"--kind NicoClusterTemplate", "--controller=false"},
			universe: map[string]string{
				"api/v1alpha1/nicoclustertemplate_types.go": "package v1alpha1\n",
			},
		},
		"refuses to overwrite existing controller without force": {
			args: []string{"--kind NicoCluster"},
			universe: map[string]string{
				"internal/controller/nicocluster_controller.go": "package controller\n",
				"controllers/nicocluster_controller.go":         "package controllers\n\nfunc existing() {}\n",
			},
			wantError: true,
		},
		"overwrites existing controller with force": {
			args: []string{"--kind NicoCluster", "--force=true"},
			universe: map[string]string{
				"internal/controller/nicocluster_controller.go": "package controller\n",
				"controllers/nicocluster_controller.go":         "package controllers\n\nfunc existing() {}\n",
			},
			wantContents: map[string]string{
				"controllers/nicocluster_controller.go": "package controllers\n",
			},
			wantRemoved: []string{"internal/controller/nicocluster_controller.go"},
		},
		"keeps existing suite test when generated duplicate differs": {
			args: []string{"--kind NicoCluster"},
			universe: map[string]string{
				"internal/controller/nicocluster_controller.go": "package controller\n",
				"internal/controller/suite_test.go":             "package controller\n\nfunc generated() {}\n",
				"controllers/suite_test.go":                     "package controllers\n\nfunc existing() {}\n",
			},
			wantContents: map[string]string{
				"controllers/nicocluster_controller.go": "package controllers\n",
				"controllers/suite_test.go":             "package controllers\n\nfunc existing() {}\n",
			},
			wantRemoved: []string{
				"internal/controller/nicocluster_controller.go",
				"internal/controller/suite_test.go",
			},
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			err := adaptCreateAPI(params.universe, params.args)
			if params.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for path, want := range params.wantContents {
				if params.universe[path] != want {
					t.Fatalf("unexpected content for %s:\n%s", path, params.universe[path])
				}
			}
			if main := params.universe["cmd/main.go"]; main != "" && strings.Contains(main, "internal/controller") {
				t.Fatalf("expected cmd/main.go to use controllers package:\n%s", main)
			}
			for _, path := range params.wantRemoved {
				if _, ok := params.universe[path]; ok {
					t.Fatalf("expected %s to be removed", path)
				}
			}
		})
	}
}

func TestUnit_ArgValue(t *testing.T) {
	type parameters struct {
		args []string
		want string
	}

	tests := map[string]parameters{
		"separate flag value": {
			args: []string{"--kind", "NicoCluster"},
			want: "NicoCluster",
		},
		"equals flag value": {
			args: []string{"--kind=NicoCluster"},
			want: "NicoCluster",
		},
		"kubebuilder external plugin combined flag value": {
			args: []string{"--kind NicoCluster"},
			want: "NicoCluster",
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			got := argValue(params.args, "--kind")
			if got != params.want {
				t.Fatalf("argValue() = %q, want %q", got, params.want)
			}
		})
	}
}
