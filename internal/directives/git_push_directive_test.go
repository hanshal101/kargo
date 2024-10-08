package directives

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sosedoff/gitkit"
	"github.com/stretchr/testify/require"

	"github.com/akuity/kargo/internal/controller/git"
	"github.com/akuity/kargo/internal/credentials"
)

func TestGitPushDirective_Validate(t *testing.T) {
	testCases := []struct {
		name             string
		config           Config
		expectedProblems []string
	}{
		{
			name:   "path not specified",
			config: Config{},
			expectedProblems: []string{
				"(root): path is required",
			},
		},
		{
			name: "path is empty string",
			config: Config{
				"path": "",
			},
			expectedProblems: []string{
				"path: String length must be greater than or equal to 1",
			},
		},
		{
			name: "just generateTargetBranch is true",
			config: Config{ // Should be completely valid
				"generateTargetBranch": true,
				"path":                 "/fake/path",
			},
		},
		{
			name: "generateTargetBranch is true and targetBranch is empty string",
			config: Config{ // Should be completely valid
				"generateTargetBranch": true,
				"path":                 "/fake/path",
				"targetBranch":         "",
			},
		},
		{
			name: "generateTargetBranch is true and targetBranch is specified",
			// These are meant to be mutually exclusive.
			config: Config{
				"generateTargetBranch": true,
				"targetBranch":         "fake-branch",
			},
			expectedProblems: []string{
				"(root): Must validate one and only one schema",
			},
		},
		{
			name:   "generateTargetBranch not specified and targetBranch not specified",
			config: Config{},
			expectedProblems: []string{
				"(root): Must validate one and only one schema",
			},
		},
		{
			name: "generateTargetBranch not specified and targetBranch is empty string",
			config: Config{
				"targetBranch": "",
			},
			expectedProblems: []string{
				"(root): Must validate one and only one schema",
			},
		},
		{
			name: "generateTargetBranch not specified and targetBranch is specified",
			config: Config{ // Should be completely valid
				"path":         "/fake/path",
				"targetBranch": "fake-branch",
			},
		},
		{
			name: "just generateTargetBranch is false",
			config: Config{
				"generateTargetBranch": false,
			},
			expectedProblems: []string{
				"(root): Must validate one and only one schema",
			},
		},
		{
			name: "generateTargetBranch is false and targetBranch is empty string",
			config: Config{
				"generateTargetBranch": false,
				"targetBranch":         "",
			},
			expectedProblems: []string{
				"(root): Must validate one and only one schema",
			},
		},
		{
			name: "generateTargetBranch is false and targetBranch is specified",
			config: Config{ // Should be completely valid
				"path":         "/fake/path",
				"targetBranch": "fake-branch",
			},
		},
	}

	d := newGitPushDirective()
	dir, ok := d.(*gitPushDirective)
	require.True(t, ok)

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := dir.validate(testCase.config)
			if len(testCase.expectedProblems) == 0 {
				require.NoError(t, err)
			} else {
				for _, problem := range testCase.expectedProblems {
					require.ErrorContains(t, err, problem)
				}
			}
		})
	}
}

func TestGitPushDirective_Run(t *testing.T) {
	// Set up a test Git server in-process
	service := gitkit.New(
		gitkit.Config{
			Dir:        t.TempDir(),
			AutoCreate: true,
		},
	)
	require.NoError(t, service.Setup())
	server := httptest.NewServer(service)
	defer server.Close()

	// This is the URL of the "remote" repository
	testRepoURL := fmt.Sprintf("%s/test.git", server.URL)

	workDir := t.TempDir()

	// Finagle a local bare repo and working tree into place the way that the
	// git-clone directive might have so we can verify the git-push directive's
	// ability to reload the working tree from the file system.
	repo, err := git.CloneBare(
		testRepoURL,
		nil,
		&git.BareCloneOptions{
			BaseDir: workDir,
		},
	)
	require.NoError(t, err)
	defer repo.Close()
	// "master" is still the default branch name for a new repository
	// unless you configure it otherwise.
	workTreePath := filepath.Join(workDir, "master")
	workTree, err := repo.AddWorkTree(
		workTreePath,
		&git.AddWorkTreeOptions{Orphan: true},
	)
	require.NoError(t, err)
	// `git worktree add` doesn't give much control over the branch name when you
	// create an orphaned working tree, so we have to follow up with this to make
	// the branch name look like what we wanted. The git-clone directive does
	// this internally as well.
	err = workTree.CreateOrphanedBranch("master")
	require.NoError(t, err)

	// Write a file. It will be the git-commit directive's job to commit it.
	err = os.WriteFile(filepath.Join(workTree.Dir(), "test.txt"), []byte("foo"), 0600)
	require.NoError(t, err)

	// Commit the changes similarly to how the git-commit directive would.
	err = workTree.AddAllAndCommit("Initial commit")
	require.NoError(t, err)

	// Now we can proceed to test the git-push directive...

	d := newGitPushDirective()
	dir, ok := d.(*gitPushDirective)
	require.True(t, ok)

	res, err := dir.run(
		context.Background(),
		&StepContext{
			Project:       "fake-project",
			Stage:         "fake-stage",
			WorkDir:       workDir,
			CredentialsDB: &credentials.FakeDB{},
		},
		GitPushConfig{
			Path:                 "master",
			GenerateTargetBranch: true,
		},
	)
	require.NoError(t, err)
	branchName, ok := res.Output.Get(branchKey)
	require.True(t, ok)
	require.Equal(t, "kargo/fake-project/fake-stage/promotion", branchName)
}
