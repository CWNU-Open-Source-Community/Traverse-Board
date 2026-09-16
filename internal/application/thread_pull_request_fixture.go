package application

import (
	"net/http"

	"cyberagent-workbench/internal/credential"
	"cyberagent-workbench/internal/domain"
	"cyberagent-workbench/internal/githubreview"
	"cyberagent-workbench/internal/repository"
)

// NewGitHubReviewServiceForTest retains real credential, approval and storage
// behavior while explicitly routing the GitHub wire protocol to one validated
// loopback fixture. Production constructors and settings expose no such route.
func NewGitHubReviewServiceForTest(store GitHubReviewStore, credentials credential.Store, executor *repository.AdvancedExecutor, capabilities domain.ExecutionPermissionRuntimeCapabilities, loopbackURL string, client *http.Client) (*GitHubReviewService, error) {
	service, err := NewGitHubReviewService(store, credentials, executor, capabilities)
	if err != nil {
		return nil, err
	}
	auth, err := githubreview.NewAuthManager(credentials, "")
	if err != nil {
		return nil, err
	}
	if _, err = githubreview.NewClientForTest(auth, loopbackURL, client); err != nil {
		return nil, err
	}
	service.clientFactory = func(auth *githubreview.AuthManager, connection githubreview.Connection) (githubReviewRemote, error) {
		return githubreview.NewClientForTestWithNetwork(auth, connection.Network, loopbackURL, client)
	}
	return service, nil
}
