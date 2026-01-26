package models

import (
	"time"
)

const (
	ScmProviderGithub    = "github"
	ScmAuthTypeGithubApp = "github_app"
	ScmAuthTypePat       = "pat"
	ScmAuthTypeOAuthApp  = "oauth_app"

	CommitAlgoVersion = 1
)

// ScmAccount stores a user's connection to a source control provider (GitHub for now).
type ScmAccount struct {
	ID              string      `gorm:"primaryKey" json:"id"`
	UserID          string      `gorm:"not null; index:idx_scm_account_user_provider,unique" json:"user_id"`
	Provider        string      `gorm:"not null; size:32; index:idx_scm_account_user_provider,unique" json:"provider"`
	AuthType        string      `gorm:"not null; size:32" json:"auth_type"`
	AccessTokenEnc  string      `gorm:"type:text" json:"access_token_enc"`
	RefreshTokenEnc string      `gorm:"type:text" json:"refresh_token_enc"`
	ExpiresAt       *CustomTime `json:"expires_at"`
	ProviderUserID  string      `gorm:"size:128" json:"provider_user_id"`
	ProviderLogin   string      `gorm:"size:255" json:"provider_login"`
	AvatarURL       string      `gorm:"size:512" json:"avatar_url"`
	CreatedAt       CustomTime  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       CustomTime  `gorm:"autoUpdateTime" json:"updated_at"`
	RevokedAt       *CustomTime `json:"revoked_at"`
}

// ScmRepository represents metadata of a repository on a provider.
type ScmRepository struct {
	ID            string     `gorm:"primaryKey" json:"id"`
	Provider      string     `gorm:"not null; size:32; index:idx_scm_repo_provider_fullname,unique; index:idx_scm_repo_provider_external,unique" json:"provider"`
	ExternalID    string     `gorm:"not null; size:64; index:idx_scm_repo_provider_external,unique" json:"external_id"`
	FullName      string     `gorm:"not null; size:255; index:idx_scm_repo_provider_fullname,unique" json:"full_name"`
	Name          string     `gorm:"size:255" json:"name"`
	Owner         string     `gorm:"size:255" json:"owner"`
	HTMLURL       string     `gorm:"size:512" json:"html_url"`
	APIURL        string     `gorm:"size:512" json:"api_url"`
	Description   string     `gorm:"type:text" json:"description"`
	Homepage      string     `gorm:"size:255" json:"homepage"`
	DefaultBranch string     `gorm:"size:255" json:"default_branch"`
	IsPrivate     bool       `gorm:"not null; default:false" json:"is_private"`
	IsFork        bool       `gorm:"not null; default:false" json:"is_fork"`
	StarCount     int        `gorm:"not null; default:0" json:"star_count"`
	ForkCount     int        `gorm:"not null; default:0" json:"fork_count"`
	WatchCount    int        `gorm:"not null; default:0" json:"watch_count"`
	CreatedAt     CustomTime `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt     CustomTime `gorm:"autoUpdateTime" json:"updated_at"`
}

// ProjectRepositoryLink ties a Wakapi project (string) to an SCM repository.
type ProjectRepositoryLink struct {
	ID             string      `gorm:"primaryKey" json:"id"`
	UserID         string      `gorm:"not null; index:idx_proj_repo_user_project,unique" json:"user_id"`
	Project        string      `gorm:"not null; size:255; index:idx_proj_repo_user_project,unique" json:"project"`
	RepositoryID   string      `gorm:"not null; index" json:"repository_id"`
	BranchOverride string      `gorm:"size:255" json:"branch_override"`
	LastSyncedAt   *CustomTime `gorm:"index" json:"last_synced_at"`
	SyncStatus     string      `gorm:"size:32; default:'pending'" json:"sync_status"`
	SyncError      string      `gorm:"type:text" json:"sync_error"`
	CreatedAt      CustomTime  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      CustomTime  `gorm:"autoUpdateTime" json:"updated_at"`
}

// ScmCommit stores commit metadata per repository and branch.
type ScmCommit struct {
	ID                 string     `gorm:"primaryKey" json:"id"`
	RepositoryID       string     `gorm:"not null; index:idx_scm_commit_repo_hash,unique; index:idx_scm_commit_repo_branch_date" json:"repository_id"`
	Hash               string     `gorm:"not null; size:255; index:idx_scm_commit_repo_hash,unique" json:"hash"`
	TruncatedHash      string     `gorm:"size:32" json:"truncated_hash"`
	Message            string     `gorm:"type:text" json:"message"`
	HTMLURL            string     `gorm:"size:512" json:"html_url"`
	URL                string     `gorm:"size:512" json:"url"`
	AuthorName         string     `gorm:"size:255" json:"author_name"`
	AuthorEmail        string     `gorm:"size:255" json:"author_email"`
	AuthorDate         CustomTime `json:"author_date"`
	AuthorUsername     string     `gorm:"size:255" json:"author_username"`
	AuthorAvatarURL    string     `gorm:"size:512" json:"author_avatar_url"`
	AuthorHTMLURL      string     `gorm:"size:512" json:"author_html_url"`
	AuthorURL          string     `gorm:"size:512" json:"author_url"`
	CommitterName      string     `gorm:"size:255" json:"committer_name"`
	CommitterEmail     string     `gorm:"size:255" json:"committer_email"`
	CommitterDate      CustomTime `gorm:"index:idx_scm_commit_repo_branch_date" json:"committer_date"`
	CommitterUsername  string     `gorm:"size:255" json:"committer_username"`
	CommitterAvatarURL string     `gorm:"size:512" json:"committer_avatar_url"`
	CommitterHTMLURL   string     `gorm:"size:512" json:"committer_html_url"`
	CommitterURL       string     `gorm:"size:512" json:"committer_url"`
	Ref                string     `gorm:"size:255" json:"ref"`
	Branch             string     `gorm:"size:255; index:idx_scm_commit_repo_branch_date" json:"branch"`
	CreatedAt          CustomTime `gorm:"autoCreateTime" json:"created_at"`
}

// CommitStat stores the computed time totals for a commit per user/project/branch.
type CommitStat struct {
	ID                           string     `gorm:"primaryKey" json:"id"`
	UserID                       string     `gorm:"not null; index:idx_commit_stats_user_project_branch_hash,unique" json:"user_id"`
	Project                      string     `gorm:"not null; size:255; index:idx_commit_stats_user_project_branch_hash,unique" json:"project"`
	RepositoryID                 string     `gorm:"not null; index" json:"repository_id"`
	Branch                       string     `gorm:"not null; size:255; index:idx_commit_stats_user_project_branch_hash,unique" json:"branch"`
	CommitHash                   string     `gorm:"not null; size:255; index:idx_commit_stats_user_project_branch_hash,unique" json:"commit_hash"`
	TotalSeconds                 float64    `gorm:"not null" json:"total_seconds"`
	HumanReadableTotal           string     `gorm:"size:64" json:"human_readable_total"`
	HumanReadableTotalWithSecond string     `gorm:"size:64" json:"human_readable_total_with_seconds"`
	CalculatedAt                 CustomTime `gorm:"autoCreateTime" json:"calculated_at"`
	AlgoVersion                  int        `gorm:"not null; default:1" json:"algo_version"`
	Dirty                        bool       `gorm:"not null; default:false; index" json:"dirty"`
	CreatedAt                    CustomTime `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt                    CustomTime `gorm:"autoUpdateTime" json:"updated_at"`
}

// CommitWindow represents the time window assigned to a commit.
type CommitWindow struct {
	Commit  *ScmCommit
	Start   time.Time
	End     time.Time
	Branch  string
	Repo    *ScmRepository
	Project string
	UserID  string
}
