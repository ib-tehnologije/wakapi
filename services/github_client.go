package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Lightweight GitHub REST client with the endpoints we need.
type GitHubClient struct {
	baseURL string
	client  *http.Client
	token   string
}

func NewGitHubClient(token string) *GitHubClient {
	return &GitHubClient{
		baseURL: "https://api.github.com",
		client:  &http.Client{Timeout: 15 * time.Second},
		token:   token,
	}
}

type githubRepo struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	HTMLURL       string `json:"html_url"`
	URL           string `json:"url"`
	Description   string `json:"description"`
	Homepage      string `json:"homepage"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Fork          bool   `json:"fork"`
	Stargazers    int    `json:"stargazers_count"`
	Forks         int    `json:"forks_count"`
	Watchers      int    `json:"watchers_count"`
}

type githubCommit struct {
	SHA    string `json:"sha"`
	HTML   string `json:"html_url"`
	Commit struct {
		Author struct {
			Name  string    `json:"name"`
			Email string    `json:"email"`
			Date  time.Time `json:"date"`
		} `json:"author"`
		Committer struct {
			Name  string    `json:"name"`
			Email string    `json:"email"`
			Date  time.Time `json:"date"`
		} `json:"committer"`
		Message string `json:"message"`
		Tree    struct {
			SHA string `json:"sha"`
			URL string `json:"url"`
		} `json:"tree"`
		URL string `json:"url"`
	} `json:"commit"`
	Author struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
		HTMLURL   string `json:"html_url"`
		URL       string `json:"url"`
	} `json:"author"`
	Committer struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
		HTMLURL   string `json:"html_url"`
		URL       string `json:"url"`
	} `json:"committer"`
	URL string `json:"url"`
}

func (c *GitHubClient) GetRepo(ctx context.Context, fullName string) (*githubRepo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s", c.baseURL, fullName), nil)
	c.addAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github get repo failed: %s", resp.Status)
	}
	var repo githubRepo
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		return nil, err
	}
	return &repo, nil
}

func (c *GitHubClient) ListCommits(ctx context.Context, fullName, branch, author string, page, perPage int) ([]*githubCommit, error) {
	u, _ := url.Parse(fmt.Sprintf("%s/repos/%s/commits", c.baseURL, fullName))
	q := u.Query()
	if branch != "" {
		q.Set("sha", branch)
	}
	if author != "" {
		q.Set("author", author)
	}
	if page > 0 {
		q.Set("page", fmt.Sprintf("%d", page))
	}
	if perPage > 0 {
		q.Set("per_page", fmt.Sprintf("%d", perPage))
	}
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	c.addAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github list commits failed: %s", resp.Status)
	}
	var commits []*githubCommit
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return nil, err
	}
	return commits, nil
}

func (c *GitHubClient) addAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
}

// ListRepos lists repositories accessible to the current token.
func (c *GitHubClient) ListRepos(ctx context.Context, page, perPage int) ([]*githubRepo, error) {
	u, _ := url.Parse(fmt.Sprintf("%s/user/repos", c.baseURL))
	q := u.Query()
	q.Set("affiliation", "owner,collaborator,organization_member")
	if page > 0 {
		q.Set("page", fmt.Sprintf("%d", page))
	}
	if perPage > 0 {
		q.Set("per_page", fmt.Sprintf("%d", perPage))
	}
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	c.addAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github list repos failed: %s", resp.Status)
	}
	var repos []*githubRepo
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, err
	}
	return repos, nil
}

// GetRepoByID retrieves repository by numeric id.
func (c *GitHubClient) GetRepoByID(ctx context.Context, id string) (*githubRepo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repositories/%s", c.baseURL, id), nil)
	c.addAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github get repo by id failed: %s", resp.Status)
	}
	var repo githubRepo
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		return nil, err
	}
	return &repo, nil
}
