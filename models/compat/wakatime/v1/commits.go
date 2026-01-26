package v1

type CommitsResponse struct {
	Commits     []*Commit      `json:"commits"`
	Author      *string        `json:"author"`
	NextPage    *int           `json:"next_page"`
	NextPageURL *string        `json:"next_page_url"`
	Page        int            `json:"page"`
	PrevPage    *int           `json:"prev_page"`
	PrevPageURL *string        `json:"prev_page_url"`
	Branch      string         `json:"branch"`
	Project     *CommitProject `json:"project"`
	Status      string         `json:"status"`
	Total       int            `json:"total"`
	TotalPages  int            `json:"total_pages"`
}

type CommitResponse struct {
	Commit  *Commit        `json:"commit"`
	Branch  string         `json:"branch"`
	Project *CommitProject `json:"project"`
	Status  string         `json:"status"`
}

type CommitProject struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Privacy    string      `json:"privacy"`
	Repository *Repository `json:"repository"`
}

type Repository struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Owner         string `json:"owner"`
	HtmlURL       string `json:"html_url"`
	URL           string `json:"url"`
	Description   string `json:"description"`
	Homepage      string `json:"homepage"`
	DefaultBranch string `json:"default_branch"`
	IsPrivate     bool   `json:"is_private"`
	IsFork        bool   `json:"is_fork"`
	StarCount     int    `json:"star_count"`
	ForkCount     int    `json:"fork_count"`
	WatchCount    int    `json:"watch_count"`
}

type Commit struct {
	AuthorAvatarURL              string  `json:"author_avatar_url"`
	AuthorDate                   string  `json:"author_date"`
	AuthorEmail                  string  `json:"author_email"`
	AuthorHTMLURL                string  `json:"author_html_url"`
	AuthorName                   string  `json:"author_name"`
	AuthorURL                    string  `json:"author_url"`
	AuthorUsername               string  `json:"author_username"`
	CommitterAvatarURL           string  `json:"committer_avatar_url"`
	CommitterDate                string  `json:"committer_date"`
	CommitterEmail               string  `json:"committer_email"`
	CommitterHTMLURL             string  `json:"committer_html_url"`
	CommitterName                string  `json:"committer_name"`
	CommitterURL                 string  `json:"committer_url"`
	CommitterUsername            string  `json:"committer_username"`
	CreatedAt                    string  `json:"created_at"`
	Hash                         string  `json:"hash"`
	TruncatedHash                string  `json:"truncated_hash"`
	HTMLURL                      string  `json:"html_url"`
	HumanReadableTotal           string  `json:"human_readable_total"`
	HumanReadableTotalWithSecond string  `json:"human_readable_total_with_seconds"`
	ID                           string  `json:"id"`
	Message                      string  `json:"message"`
	Ref                          string  `json:"ref"`
	TotalSeconds                 float64 `json:"total_seconds"`
	URL                          string  `json:"url"`
	Branch                       string  `json:"branch"`
}
