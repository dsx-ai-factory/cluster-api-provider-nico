// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
)

// ClientCache reuses NICo clients for identical Secret revisions so OAuth token reuse survives across reconciles.
type ClientCache struct {
	mu      sync.RWMutex
	clients map[string]*Client
}

func NewClientCache() *ClientCache {
	return &ClientCache{
		clients: map[string]*Client{},
	}
}

func (c *ClientCache) GetOrCreate(ctx context.Context, secret *corev1.Secret, cfg SecretConfig) (*Client, error) {
	key, prefix, err := cacheKey(secret)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	if client, ok := c.clients[key]; ok {
		c.mu.RUnlock()
		return client, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if client, ok := c.clients[key]; ok {
		return client, nil
	}

	client, err := NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	for existingKey := range c.clients {
		if len(existingKey) >= len(prefix) && existingKey[:len(prefix)] == prefix && existingKey != key {
			delete(c.clients, existingKey)
		}
	}
	c.clients[key] = client
	return client, nil
}

func cacheKey(secret *corev1.Secret) (string, string, error) {
	if secret == nil {
		return "", "", fmt.Errorf("identity secret is nil")
	}
	if secret.Namespace == "" || secret.Name == "" {
		return "", "", fmt.Errorf("identity secret must include namespace and name")
	}
	if secret.ResourceVersion == "" {
		return "", "", fmt.Errorf("identity secret must include resourceVersion")
	}

	prefix := secret.Namespace + "/" + secret.Name + "@"
	return prefix + secret.ResourceVersion, prefix, nil
}
