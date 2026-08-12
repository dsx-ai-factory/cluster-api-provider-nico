// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const apiVersion = "v1alpha1"

type pluginRequest struct {
	APIVersion  string            `json:"apiVersion"`
	Args        []string          `json:"args"`
	Command     string            `json:"command"`
	Universe    map[string]string `json:"universe"`
	PluginChain []string          `json:"pluginChain,omitempty"`
	Config      map[string]any    `json:"config,omitempty"`
}

type pluginResponse struct {
	APIVersion string            `json:"apiVersion"`
	Command    string            `json:"command"`
	Metadata   metadata          `json:"metadata"`
	Universe   map[string]string `json:"universe"`
	Error      bool              `json:"error,omitempty"`
	ErrorMsgs  []string          `json:"errorMsgs,omitempty"`
	Flags      []flag            `json:"flags,omitempty"`
}

type metadata struct {
	Description string `json:"description,omitempty"`
	Examples    string `json:"examples,omitempty"`
}

type flag struct {
	Name    string `json:"Name"`
	Type    string `json:"Type"`
	Default string `json:"Default"`
	Usage   string `json:"Usage"`
}

func main() {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		writeError(apiVersion, "", err)
		return
	}

	var req pluginRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(apiVersion, "", err)
		return
	}

	res := pluginResponse{
		APIVersion: req.APIVersion,
		Command:    req.Command,
		Universe:   clone(req.Universe),
	}

	switch req.Command {
	case "metadata":
		res.Metadata = metadata{
			Description: "Adapts Kubebuilder go/v4 controller scaffolding to the CAPNICo layout.",
			Examples:    "kubebuilder create api --group infrastructure --version v1alpha1 --kind NicoCluster --resource=true --controller=true --make=false",
		}
	case "flags":
		res.Flags = nil
	case "create api":
		if err := adaptCreateAPI(res.Universe, req.Args); err != nil {
			res.Error = true
			res.ErrorMsgs = []string{err.Error()}
		}
	}

	writeJSON(res)
}

func adaptCreateAPI(universe map[string]string, args []string) error {
	if !boolArg(args, "--controller", true) {
		return nil
	}

	kind := argValue(args, "--kind")
	if kind == "" {
		return fmt.Errorf("--kind is required")
	}

	stem := strings.ToLower(kind)
	from := filepath.ToSlash(filepath.Join("internal", "controller", stem+"_controller.go"))
	to := filepath.ToSlash(filepath.Join("controllers", stem+"_controller.go"))

	content, ok := universe[from]
	if !ok {
		return nil
	}

	adapted := adaptController(content)
	if existing, ok := universe[to]; ok && existing != adapted && !boolArg(args, "--force", false) {
		return fmt.Errorf("%s already exists; pass --force to overwrite", to)
	}

	universe[to] = adapted
	if err := moveGeneratedFile(universe, filepath.ToSlash(filepath.Join("internal", "controller", stem+"_controller_test.go")), filepath.ToSlash(filepath.Join("controllers", stem+"_controller_test.go")), boolArg(args, "--force", false)); err != nil {
		return err
	}
	if err := moveGeneratedFile(universe, filepath.ToSlash(filepath.Join("internal", "controller", "suite_test.go")), filepath.ToSlash(filepath.Join("controllers", "suite_test.go")), boolArg(args, "--force", false)); err != nil {
		return err
	}
	if main, ok := universe["cmd/main.go"]; ok {
		universe["cmd/main.go"] = adaptMain(main)
	}
	removeGeneratedFile(universe, from)

	return nil
}

func adaptController(content string) string {
	return strings.Replace(content, "package controller", "package controllers", 1)
}

func moveGeneratedFile(universe map[string]string, from, to string, force bool) error {
	content, ok := universe[from]
	if !ok {
		return nil
	}

	adapted := adaptController(content)
	if existing, ok := universe[to]; ok {
		if existing != adapted && !force {
			removeGeneratedFile(universe, from)
			return nil
		}
	}

	universe[to] = adapted
	removeGeneratedFile(universe, from)
	return nil
}

func adaptMain(content string) string {
	content = strings.ReplaceAll(content, "/internal/controller", "/controllers")
	return strings.ReplaceAll(content, "controller.", "controllers.")
}

func removeGeneratedFile(universe map[string]string, path string) {
	delete(universe, path)
	_ = os.Remove(path)
}

func boolArg(args []string, name string, defaultValue bool) bool {
	value := argValue(args, name)
	if value == "" {
		return defaultValue
	}
	return value == "true"
}

func argValue(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"=")
		}
		if strings.HasPrefix(arg, name+" ") {
			return strings.TrimSpace(strings.TrimPrefix(arg, name+" "))
		}
	}
	return ""
}

func clone(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func writeError(version, command string, err error) {
	writeJSON(pluginResponse{
		APIVersion: version,
		Command:    command,
		Universe:   map[string]string{},
		Error:      true,
		ErrorMsgs:  []string{err.Error()},
	})
}

func writeJSON(res pluginResponse) {
	_ = json.NewEncoder(os.Stdout).Encode(res)
}
