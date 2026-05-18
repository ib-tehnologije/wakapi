package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/gofrs/uuid/v5"
	"github.com/muety/wakapi/models"
)

type codexTaskSessionRepository interface {
	Upsert(*models.CodexTaskSession) error
	GetByUserWithin(string, *time.Time, *time.Time, string) ([]*models.CodexTaskSession, error)
}

type ICodexTaskService interface {
	UpsertMany(*models.User, []*CodexTaskSessionInput) ([]*models.CodexTaskSession, error)
	GetWorklogs(*models.User, *time.Time, *time.Time, string) ([]*CodexTaskWorklog, error)
}

type CodexTaskSessionInput struct {
	ExternalKey           string
	Project               string
	WorkspaceRoot         string
	Repository            string
	Branch                string
	StartedAt             time.Time
	EndedAt               *time.Time
	DurationSeconds       float64
	Status                string
	SummaryHR             string
	Prompt                string
	LastAssistantMessage  string
	Evidence              []string
	TechnicalEvidenceJSON string
}

type CodexTaskWorklog struct {
	ID              string
	ExternalKey     string
	Project         string
	Source          string
	StartedAt       time.Time
	EndedAt         time.Time
	DurationSeconds float64
	Summary         string
	TechnicalNote   string
	WorkspaceRoot   string
	Repository      string
	Branch          string
	Status          string
}

type CodexTaskService struct {
	repository codexTaskSessionRepository
}

var (
	codexFencedCodePattern   = regexp.MustCompile("(?s)```.*?```")
	codexInlineCodePattern   = regexp.MustCompile("`([^`]+)`")
	codexMarkdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	codexHeadingPattern      = regexp.MustCompile(`^#{1,6}\s+`)
	codexListPattern         = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	codexWhitespacePattern   = regexp.MustCompile(`\s+`)
	codexEvidenceFilePattern = regexp.MustCompile(`(?:^|[\s"'=:(])((?:\.{1,2}/)?[A-Za-z0-9._@~+-][A-Za-z0-9._@~+/-]*\.(?:cs|go|mjs|cjs|js|jsx|ts|tsx|json|ya?ml|toml|sql|pas|dfm|dart|md|sh|bash|zsh|ps1|csproj|sln|props|targets|graphql|proto|rs|py|rb|php|java|kt|swift|css|scss|html|xml|txt|ini|conf|env|service))(?:[:#]\d+)?`)
	codexPatchFilePattern    = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)
	codexReplyPrefixPattern  = regexp.MustCompile(`^(?:you|you're|you are|your|i|i'm|i am|i've|i have|we|we're|we are)\b`)
	codexVagueSummaryPattern = regexp.MustCompile(`^(?:checked|patched|fixed|updated|changed|reviewed|worked on|handled|investigated|debugged|cleaned)(?:\s+(?:it|this|that))?[.!?]?$|^(?:checked and patched|checked and fixed|patched and checked|fixed and checked)\s+(?:it|this|that)[.!?]?$`)
	codexPatchAppliedPattern = regexp.MustCompile(`^(?:patch applied successfully|corrective patch is applied(?: and verified)?)[.!?]?$`)
	codexFillerSummaries     = map[string]bool{"yes": true, "yep": true, "ok": true, "okay": true, "done": true, "sure": true, "youreright": true, "youareright": true}
)

func NewCodexTaskService(repository codexTaskSessionRepository) *CodexTaskService {
	return &CodexTaskService{repository: repository}
}

func (s *CodexTaskService) UpsertMany(user *models.User, inputs []*CodexTaskSessionInput) ([]*models.CodexTaskSession, error) {
	if user == nil || user.ID == "" {
		return nil, errors.New("user is required")
	}

	result := make([]*models.CodexTaskSession, 0, len(inputs))
	for _, input := range inputs {
		session, err := s.buildSession(user, input)
		if err != nil {
			return nil, err
		}
		if err := s.repository.Upsert(session); err != nil {
			return nil, err
		}
		result = append(result, session)
	}

	return result, nil
}

func (s *CodexTaskService) GetWorklogs(user *models.User, from, to *time.Time, project string) ([]*CodexTaskWorklog, error) {
	sessions, err := s.repository.GetByUserWithin(user.ID, from, to, project)
	if err != nil {
		return nil, err
	}

	worklogs := make([]*CodexTaskWorklog, 0, len(sessions))
	for _, session := range sessions {
		if session.EndedAt == nil {
			continue
		}
		worklogs = append(worklogs, &CodexTaskWorklog{
			ID:              session.ID,
			ExternalKey:     session.ExternalKey,
			Project:         session.Project,
			Source:          models.CodexTaskWorklogSource,
			StartedAt:       session.StartedAt.T(),
			EndedAt:         session.EndedAt.T(),
			DurationSeconds: session.DurationSeconds,
			Summary:         session.SummaryHR,
			TechnicalNote:   session.TechnicalNote,
			WorkspaceRoot:   session.WorkspaceRoot,
			Repository:      session.Repository,
			Branch:          session.Branch,
			Status:          session.Status,
		})
	}
	return worklogs, nil
}

func (s *CodexTaskService) buildSession(user *models.User, input *CodexTaskSessionInput) (*models.CodexTaskSession, error) {
	if input == nil {
		return nil, errors.New("session is required")
	}

	input.ExternalKey = strings.TrimSpace(input.ExternalKey)
	input.Project = strings.TrimSpace(input.Project)
	if input.ExternalKey == "" {
		return nil, errors.New("external_key is required")
	}
	if input.Project == "" {
		return nil, errors.New("project is required")
	}
	if input.StartedAt.IsZero() {
		return nil, errors.New("started_at is required")
	}
	if input.EndedAt != nil && input.EndedAt.Before(input.StartedAt) {
		return nil, errors.New("ended_at must be after started_at")
	}

	duration := input.DurationSeconds
	if duration <= 0 && input.EndedAt != nil {
		duration = input.EndedAt.Sub(input.StartedAt).Seconds()
	}
	if duration < 0 {
		duration = 0
	}

	status := strings.TrimSpace(input.Status)
	if status == "" {
		if input.EndedAt == nil {
			status = models.CodexTaskSessionStatusOpen
		} else {
			status = models.CodexTaskSessionStatusClosed
		}
	}

	summary := usefulCodexSummary(input.SummaryHR, 220)
	if summary == "" {
		summary = buildCodexSummary(input)
	}

	technicalNote := buildCodexTechnicalNote(input)

	var endedAt *models.CustomTime
	if input.EndedAt != nil {
		custom := models.CustomTime(*input.EndedAt)
		endedAt = &custom
	}

	return &models.CodexTaskSession{
		ID:                   uuid.Must(uuid.NewV4()).String(),
		User:                 user,
		UserID:               user.ID,
		ExternalKey:          input.ExternalKey,
		Project:              input.Project,
		WorkspaceRoot:        strings.TrimSpace(input.WorkspaceRoot),
		Repository:           strings.TrimSpace(input.Repository),
		Branch:               strings.TrimSpace(input.Branch),
		StartedAt:            models.CustomTime(input.StartedAt),
		EndedAt:              endedAt,
		DurationSeconds:      duration,
		Status:               status,
		SummaryHR:            summary,
		Prompt:               strings.TrimSpace(input.Prompt),
		LastAssistantMessage: strings.TrimSpace(input.LastAssistantMessage),
		EvidenceJSON:         strings.TrimSpace(input.TechnicalEvidenceJSON),
		TechnicalNote:        technicalNote,
	}, nil
}

func buildCodexSummary(input *CodexTaskSessionInput) string {
	if summary := codexEvidenceSummary(input); summary != "" {
		return summary
	}

	if summary := assistantFallbackSummary(input.LastAssistantMessage, 180); summary != "" {
		return summary
	}

	project := strings.TrimSpace(input.Project)
	if project == "" {
		project = "nepoznatom projektu"
	}

	return fmt.Sprintf("Rad s Codexom na projektu %s.", project)
}

func assistantFallbackSummary(value string, max int) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}

	if jsonSummary := summaryFromJSON(raw); jsonSummary != "" {
		return usefulCodexSummary(ensureCodexSentence(jsonSummary), max)
	}

	return ""
}

func codexEvidenceSummary(input *CodexTaskSessionInput) string {
	changedFiles := []string{}
	inspectedFiles := []string{}
	commands := []string{}

	for _, item := range input.Evidence {
		evidence := strings.TrimSpace(item)
		if evidence == "" {
			continue
		}
		if strings.HasPrefix(evidence, "command:") {
			command := strings.TrimSpace(strings.TrimPrefix(evidence, "command:"))
			commands = append(commands, command)
			addCodexEvidenceFiles(&inspectedFiles, extractCodexCommandFiles(command))
			continue
		}
		addCodexEvidenceFiles(&changedFiles, []string{evidence})
	}

	for _, event := range codexTechnicalEvidenceEvents(input.TechnicalEvidenceJSON) {
		command := strings.TrimSpace(event.Command)
		signal := strings.TrimSpace(strings.TrimSpace(event.ToolName) + " " + command)
		if signal != "" {
			commands = append(commands, signal)
		}
		patchFiles := extractCodexPatchFiles(command)
		if len(patchFiles) > 0 || event.ToolName == "apply_patch" {
			addCodexEvidenceFiles(&changedFiles, patchFiles)
			continue
		}
		if command == "" {
			continue
		}
		addCodexEvidenceFiles(&inspectedFiles, extractCodexCommandFiles(command))
	}

	if len(changedFiles) > 0 {
		return codexFileSummary("Updated", changedFiles, 1, 180)
	}
	if len(inspectedFiles) > 0 {
		return codexFileSummary("Inspected", inspectedFiles, 2, 180)
	}
	if len(commands) > 0 {
		return codexCommandCategorySummary(commands)
	}
	return ""
}

func codexCommandCategorySummary(commands []string) string {
	joined := strings.ToLower(strings.Join(commands, "\n"))
	if regexp.MustCompile(`\bkubectl\b`).MatchString(joined) {
		return "Checked Kubernetes resources."
	}
	if regexp.MustCompile(`\b(psql|sqlcmd|execute_sql|mcp__mssql)\b`).MatchString(joined) {
		return "Checked database state."
	}
	if regexp.MustCompile(`(?:db_query|database_query|company_db_query)`).MatchString(joined) {
		return "Checked database state."
	}
	if regexp.MustCompile(`\b(gh\s+(run|workflow|actions?)|git\s+)`).MatchString(joined) {
		return "Checked repository state."
	}
	if regexp.MustCompile(`\b(npm|yarn|pnpm|dotnet|go)\s+(test|build|run)\b`).MatchString(joined) {
		return "Ran project checks."
	}
	return ""
}

type codexEvidenceEvent struct {
	ToolName string `json:"tool_name"`
	Command  string `json:"command"`
	Cmd      string `json:"cmd"`
}

func codexTechnicalEvidenceEvents(value string) []codexEvidenceEvent {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	var payload struct {
		Events []codexEvidenceEvent `json:"events"`
	}
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return nil
	}
	for i := range payload.Events {
		if payload.Events[i].Command == "" {
			payload.Events[i].Command = payload.Events[i].Cmd
		}
	}
	return payload.Events
}

func extractCodexPatchFiles(command string) []string {
	files := []string{}
	for _, match := range codexPatchFilePattern.FindAllStringSubmatch(command, -1) {
		if len(match) > 1 {
			addCodexEvidenceFiles(&files, []string{match[1]})
		}
	}
	return files
}

func extractCodexCommandFiles(command string) []string {
	files := []string{}
	for _, match := range codexEvidenceFilePattern.FindAllStringSubmatch(command, -1) {
		if len(match) > 1 {
			addCodexEvidenceFiles(&files, []string{match[1]})
		}
	}
	return files
}

func addCodexEvidenceFiles(target *[]string, values []string) {
	for _, value := range values {
		file := cleanCodexEvidenceFile(value)
		if file == "" {
			continue
		}
		exists := false
		for _, existing := range *target {
			if existing == file {
				exists = true
				break
			}
		}
		if !exists {
			*target = append(*target, file)
		}
	}
}

func cleanCodexEvidenceFile(value string) string {
	file := strings.Trim(strings.TrimSpace(value), "\"'`,.;)")
	file = strings.TrimPrefix(file, "./")
	if file == "" || strings.Contains(file, "://") || strings.Contains(file, "node_modules/") || strings.Contains(file, "/.git/") {
		return ""
	}
	return file
}

func codexFileSummary(verb string, files []string, maxFiles int, maxChars int) string {
	cleanFiles := []string{}
	addCodexEvidenceFiles(&cleanFiles, files)
	if len(cleanFiles) == 0 {
		return ""
	}
	if maxFiles <= 0 || maxFiles > len(cleanFiles) {
		maxFiles = len(cleanFiles)
	}

	label := cleanFiles[0]
	if maxFiles > 1 {
		label = cleanFiles[0] + " and " + cleanFiles[1]
	}
	summary := fmt.Sprintf("%s %s.", verb, label)
	if len([]rune(summary)) <= maxChars {
		return summary
	}
	return fmt.Sprintf("%s %s.", verb, filepath.Base(cleanFiles[0]))
}

func summaryFromJSON(value string) string {
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return ""
	}

	var payload struct {
		Title   string `json:"title"`
		Message string `json:"message"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return ""
	}
	for _, value := range []string{payload.Title, payload.Message, payload.Summary} {
		if value := strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cleanCodexSummaryText(value string) string {
	value = codexMarkdownLinkPattern.ReplaceAllString(value, "$1")
	value = codexInlineCodePattern.ReplaceAllString(value, "$1")

	lines := []string{}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		line = codexHeadingPattern.ReplaceAllString(line, "")
		line = codexListPattern.ReplaceAllString(line, "")
		line = strings.Trim(line, "*_~ ")
		if line != "" {
			lines = append(lines, line)
		}
	}

	return strings.TrimSpace(codexWhitespacePattern.ReplaceAllString(strings.Join(lines, " "), " "))
}

func firstCodexSentence(value string) string {
	for index, r := range value {
		if r == '.' || r == '?' || r == '!' {
			return strings.TrimSpace(value[:index+len(string(r))])
		}
	}
	return strings.TrimSpace(value)
}

func ensureCodexSentence(value string) string {
	text := normalizeCodexSummary(value, 0)
	if text == "" {
		return ""
	}

	runes := []rune(text)
	if strings.ContainsRune(".?!", runes[len(runes)-1]) {
		return text
	}
	return text + "."
}

func normalizeCodexSummary(value string, max int) string {
	summary := strings.Trim(strings.TrimSpace(value), "\"'`")
	summary = strings.TrimSpace(codexWhitespacePattern.ReplaceAllString(summary, " "))
	if summary == "" {
		return ""
	}
	if max <= 0 {
		return summary
	}

	runes := []rune(summary)
	if len(runes) <= max {
		return summary
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}

func usefulCodexSummary(value string, max int) string {
	summary := normalizeCodexSummary(value, max)
	plain := strings.Builder{}
	for _, r := range summary {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			plain.WriteRune(unicode.ToLower(r))
		}
	}
	key := plain.String()
	if key == "" || codexFillerSummaries[key] || !isUsefulCodexWorkSummary(summary) {
		return ""
	}
	return summary
}

func isUsefulCodexWorkSummary(value string) bool {
	summary := strings.TrimSpace(value)
	if summary == "" {
		return false
	}

	lower := strings.ToLower(summary)
	if lower == "..." || strings.HasPrefix(lower, "rad s codexom na projektu ") {
		return false
	}
	if codexReplyPrefixPattern.MatchString(lower) {
		return false
	}
	if strings.HasPrefix(lower, "yes ") || strings.HasPrefix(lower, "yep ") ||
		strings.HasPrefix(lower, "no ") || strings.HasPrefix(lower, "ok ") ||
		strings.HasPrefix(lower, "okay ") || strings.HasPrefix(lower, "sure ") ||
		strings.HasPrefix(lower, "done ") || strings.HasPrefix(lower, "right ") ||
		strings.HasPrefix(lower, "exactly ") || strings.HasPrefix(lower, "correct ") {
		return false
	}
	if strings.HasPrefix(lower, "good") || strings.HasPrefix(lower, "great") {
		return false
	}
	if codexVagueSummaryPattern.MatchString(lower) {
		return false
	}
	if codexPatchAppliedPattern.MatchString(lower) {
		return false
	}
	return true
}

func buildCodexTechnicalNote(input *CodexTaskSessionInput) string {
	parts := []string{}
	if len(input.Evidence) > 0 {
		parts = append(parts, fmt.Sprintf("Codex evidence: %d captured items (%s).", len(input.Evidence), strings.Join(limitStrings(input.Evidence, 8), ", ")))
	}
	if strings.TrimSpace(input.WorkspaceRoot) != "" {
		parts = append(parts, fmt.Sprintf("Workspace: %s.", strings.TrimSpace(input.WorkspaceRoot)))
	}
	if strings.TrimSpace(input.Branch) != "" {
		parts = append(parts, fmt.Sprintf("Branch: %s.", strings.TrimSpace(input.Branch)))
	}
	if len(parts) == 0 {
		parts = append(parts, "Codex evidence: no captured tool evidence.")
	}
	return strings.Join(parts, " ")
}

func limitStrings(values []string, max int) []string {
	seen := map[string]bool{}
	result := make([]string, 0, max)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == max {
			break
		}
	}
	return result
}

func EncodeCodexEvidence(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
