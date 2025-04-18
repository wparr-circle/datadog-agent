// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package repository

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestRepositories(t *testing.T) *Repositories {
	rootPath := t.TempDir()
	repositories := NewRepositories(rootPath, nil)
	return repositories
}

func TestRepositoriesEmpty(t *testing.T) {
	repositories := newTestRepositories(t)

	state, err := repositories.GetStates()
	assert.NoError(t, err)
	assert.Empty(t, state)
}

func TestRepositories(t *testing.T) {
	repositories := newTestRepositories(t)

	err := repositories.Create(context.Background(), "repo1", "v1", t.TempDir())
	assert.NoError(t, err)
	repository := repositories.Get("repo1")
	err = repository.SetExperiment(context.Background(), "v2", t.TempDir())
	assert.NoError(t, err)
	err = repositories.Create(context.Background(), "repo2", "v1.0", t.TempDir())
	assert.NoError(t, err)

	state, err := repositories.GetStates()
	assert.NoError(t, err)
	assert.Len(t, state, 2)
	assert.Equal(t, state["repo1"], State{Stable: "v1", Experiment: "v2"})
	assert.Equal(t, state["repo2"], State{Stable: "v1.0"})
}

func TestRepositoriesReopen(t *testing.T) {
	repositories := newTestRepositories(t)
	err := repositories.Create(context.Background(), "repo1", "v1", t.TempDir())
	assert.NoError(t, err)
	err = repositories.Create(context.Background(), "repo2", "v1", t.TempDir())
	assert.NoError(t, err)

	repositories = NewRepositories(repositories.rootPath, nil)

	state, err := repositories.GetStates()
	assert.NoError(t, err)
	assert.Len(t, state, 2)
	assert.Equal(t, state["repo1"], State{Stable: "v1"})
	assert.Equal(t, state["repo2"], State{Stable: "v1"})
}

func TestLoadRepositories(t *testing.T) {
	rootDir := t.TempDir()

	os.Mkdir(path.Join(rootDir, "datadog-agent"), 0755)
	os.Mkdir(path.Join(rootDir, tempDirPrefix+"2394812349"), 0755)
	os.Mkdir(path.Join(rootDir, "run"), 0755)
	os.Mkdir(path.Join(rootDir, "tmp"), 0755)

	repositories, err := NewRepositories(rootDir, nil).loadRepositories()
	assert.NoError(t, err)
	assert.Len(t, repositories, 1)
	assert.Contains(t, repositories, "datadog-agent")
	assert.NotContains(t, repositories, tempDirPrefix+"2394812349")
	assert.NotContains(t, repositories, "run")
	assert.NotContains(t, repositories, "tmp")
}
