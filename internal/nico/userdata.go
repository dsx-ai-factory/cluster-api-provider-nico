package nico

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v4"
)

// InjectHostnameCloudConfig prepends hostname-related fields while preserving existing cloud-config headers.
func InjectHostnameCloudConfig(userData, hostname string) (string, error) {
	doc, err := parseYAMLDocument(userData)
	if err != nil {
		return "", fmt.Errorf("parse cloud-config: %w", err)
	}

	root := documentRootMap(doc)
	if root == nil {
		return "", fmt.Errorf("cloud-config missing root mapping")
	}

	headerComment := collectHeaderComment(doc, root)
	prependMapValue(root, "manage_etc_hosts", scalarBool(true))
	prependMapValue(root, "preserve_hostname", scalarBool(false))
	prependMapValue(root, "hostname", scalarString(hostname))
	if headerComment != "" {
		doc.HeadComment = headerComment
	}

	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return "", fmt.Errorf("encode cloud-config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode cloud-config: %w", err)
	}

	return out.String(), nil
}

func parseYAMLDocument(in string) (*yaml.Node, error) {
	var doc yaml.Node
	dec := yaml.NewDecoder(strings.NewReader(in))
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func collectHeaderComment(doc, root *yaml.Node) string {
	if doc == nil || root == nil || root.Kind != yaml.MappingNode || len(root.Content) == 0 {
		return ""
	}

	firstKey := root.Content[0]
	if firstKey == nil {
		return ""
	}

	parts := make([]string, 0, 2)
	if doc.HeadComment != "" {
		parts = append(parts, doc.HeadComment)
	}
	if firstKey.HeadComment != "" {
		parts = append(parts, firstKey.HeadComment)
		firstKey.HeadComment = ""
	}
	return strings.Join(parts, "\n")
}

func documentRootMap(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		if doc.Content[0].Kind == yaml.MappingNode {
			return doc.Content[0]
		}
	}
	if doc.Kind == yaml.MappingNode {
		return doc
	}
	return nil
}

func mapIndex(m *yaml.Node, key string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return i
		}
	}
	return -1
}

func prependMapValue(m *yaml.Node, key string, value *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}

	i := mapIndex(m, key)
	if i != -1 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
	}

	m.Content = append([]*yaml.Node{scalarString(key), value}, m.Content...)
}

func scalarString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func scalarBool(value bool) *yaml.Node {
	if value {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}
}
