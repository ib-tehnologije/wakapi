package services

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/gofrs/uuid/v5"
	"github.com/muety/wakapi/models"
)

type codexTaskSessionRepository interface {
	Upsert(*models.CodexTaskSession) error
	GetByUserWithin(string, *time.Time, *time.Time, string) ([]*models.CodexTaskSession, error)
	GetByUserExternalKey(string, string) (*models.CodexTaskSession, error)
	ListByUserForReview(string, *time.Time, *time.Time, string, int) ([]*models.CodexTaskSession, error)
}

type ICodexTaskService interface {
	UpsertMany(*models.User, []*CodexTaskSessionInput) ([]*models.CodexTaskSession, error)
	GetWorklogs(*models.User, *time.Time, *time.Time, string) ([]*CodexTaskWorklog, error)
	ListReviewQueue(*models.User, *time.Time, *time.Time, string, string, int) ([]*models.CodexTaskSession, error)
	ReviewSession(*models.User, *CodexTaskReviewInput) (*models.CodexTaskSession, error)
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
	SummaryHROriginal     string
	SummaryHRNormalized   string
	SummarySource         string
	SummaryConfidence     float64
	ClientMessageHR       string
	InternalMessageHR     string
	ReviewStatus          string
	Prompt                string
	LastAssistantMessage  string
	Evidence              []string
	TechnicalEvidenceJSON string
}

type CodexTaskWorklog struct {
	ID                  string
	ExternalKey         string
	Project             string
	Source              string
	StartedAt           time.Time
	EndedAt             time.Time
	DurationSeconds     float64
	Summary             string
	SummaryHROriginal   string
	SummaryHRNormalized string
	SummarySource       string
	SummaryConfidence   float64
	ClientMessageHR     string
	InternalMessageHR   string
	ReviewStatus        string
	TechnicalNote       string
	WorkspaceRoot       string
	Repository          string
	Branch              string
	Status              string
}

type CodexTaskReviewInput struct {
	ExternalKey       string
	Action            string
	ClientMessageHR   string
	InternalMessageHR string
}

type CodexTaskService struct {
	repository codexTaskSessionRepository
}

var (
	codexFencedCodePattern    = regexp.MustCompile("(?s)```.*?```")
	codexInlineCodePattern    = regexp.MustCompile("`([^`]+)`")
	codexMarkdownLinkPattern  = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	codexHeadingPattern       = regexp.MustCompile(`^#{1,6}\s+`)
	codexListPattern          = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	codexWhitespacePattern    = regexp.MustCompile(`\s+`)
	codexEvidenceFilePattern  = regexp.MustCompile(`(?:^|[\s"'=:(])((?:\.{1,2}/)?[A-Za-z0-9._@~+-][A-Za-z0-9._@~+/-]*\.(?:cs|go|mjs|cjs|js|jsx|ts|tsx|json|ya?ml|toml|sql|pas|dfm|dart|md|sh|bash|zsh|ps1|csproj|sln|props|targets|graphql|proto|rs|py|rb|php|java|kt|swift|css|scss|html|xml|txt|ini|conf|env|service))(?:[:#]\d+)?`)
	codexPatchFilePattern     = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)
	codexReplyPrefixPattern   = regexp.MustCompile(`^(?:you|you're|you are|your|i|i'm|i am|i've|i have|we|we're|we are)\b`)
	codexVagueSummaryPattern  = regexp.MustCompile(`^(?:checked|patched|fixed|updated|changed|reviewed|worked on|handled|investigated|debugged|cleaned)(?:\s+(?:it|this|that))?[.!?]?$|^(?:checked and patched|checked and fixed|patched and checked|fixed and checked)\s+(?:it|this|that)[.!?]?$`)
	codexPatchAppliedPattern  = regexp.MustCompile(`^(?:patch applied successfully|corrective patch is applied(?: and verified)?)[.!?]?$`)
	codexTicketPattern        = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b`)
	codexGenericClientPattern = regexp.MustCompile(`(?i)(?:\bvrijeme bez commitova?\b|\bvrijeme bez commita\b|\brad na projektu\b|\banaliza podataka u bazi\b|\brad na deployu\b|\brad na testovima(?: i provjerama)?\b|codex sesija bez zabilje(?:ž|z)enog konteksta|codex aktivnost bez dovoljno konteksta za opis)`)
	codexFillerSummaries      = map[string]bool{"yes": true, "yep": true, "ok": true, "okay": true, "done": true, "sure": true, "youreright": true, "youareright": true}
	codexCroatianTokens       = []string{"č", "ć", "đ", "š", "ž", "rad", "ažuriran", "azuriran", "pregledan", "provjeren", "dodan", "dodana", "dodano", "dodane", "popravljen", "popravljena", "popravljeno", "uklonjen", "uklonjena", "obrisan", "obrisani", "istražen", "istrazen", "pokrenut", "generiran", "implementiran", "integracij", "validacij", "provjerama", "obradi", "deployu", "sinkronizacij", "sesija", "sažetak", "sazetak", "stanje", "baze", "podataka", "resursi", "repozitorij", "repozitorija", "migracij", "tijek", "skrivan", "commitan", "pushan"}
)

const codexNoEvidenceSummary = "Codex sesija bez zabilježenog konteksta."
const codexMinNoEvidenceWorklogSeconds = 30.0
const codexGroupingGap = 30 * time.Minute
const codexReviewQueueMaxDefault = 200
const codexReviewQueueMinConfidence = 0.60

const (
	codexReviewStatusNeedsReview   = "needs_review"
	codexReviewStatusNeedsGrouping = "needs_grouping"
	codexReviewStatusPendingReview = "pending_review"
	codexReviewStatusApproved      = "approved"
	codexReviewStatusRejected      = "rejected"
	codexReviewStatusInternalOnly  = "internal_only"
	codexReviewStatusAutoOK        = "auto_ok"
)

func NewCodexTaskService(repository codexTaskSessionRepository) *CodexTaskService {
	return &CodexTaskService{repository: repository}
}

func (s *CodexTaskService) ListReviewQueue(user *models.User, from, to *time.Time, project string, status string, limit int) ([]*models.CodexTaskSession, error) {
	if user == nil || user.ID == "" {
		return nil, errors.New("user is required")
	}

	if limit <= 0 || limit > codexReviewQueueMaxDefault {
		limit = codexReviewQueueMaxDefault
	}

	sessions, err := s.repository.ListByUserForReview(user.ID, from, to, project, limit)
	if err != nil {
		return nil, err
	}

	filter := strings.TrimSpace(strings.ToLower(status))
	if filter == "" {
		filter = "pending"
	}

	result := make([]*models.CodexTaskSession, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		switch filter {
		case "all":
			result = append(result, session)
		case "pending":
			if codexSessionNeedsReview(session) {
				result = append(result, session)
			}
		default:
			if strings.TrimSpace(strings.ToLower(session.ReviewStatus)) == filter {
				result = append(result, session)
			}
		}
	}

	return result, nil
}

func (s *CodexTaskService) ReviewSession(user *models.User, input *CodexTaskReviewInput) (*models.CodexTaskSession, error) {
	if user == nil || user.ID == "" {
		return nil, errors.New("user is required")
	}
	if input == nil {
		return nil, errors.New("review input is required")
	}

	externalKey := strings.TrimSpace(input.ExternalKey)
	if externalKey == "" {
		return nil, errors.New("external_key is required")
	}

	session, err := s.repository.GetByUserExternalKey(user.ID, externalKey)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errors.New("session not found")
	}

	action := strings.TrimSpace(strings.ToLower(input.Action))
	switch action {
	case "approve":
		candidate := usefulCodexSummary(input.ClientMessageHR, 220)
		if candidate == "" {
			candidate = usefulCodexSummary(session.ClientMessageHR, 220)
		}
		if candidate == "" {
			candidate = usefulCodexSummary(session.SummaryHRNormalized, 220)
		}
		if candidate == "" {
			candidate = usefulCodexSummary(session.SummaryHROriginal, 220)
		}
		if candidate == "" || isGenericCodexClientMessage(candidate) {
			return nil, errors.New("approved client message is required")
		}
		session.ClientMessageHR = candidate
		session.SummaryHR = candidate
		session.ReviewStatus = codexReviewStatusApproved
		if session.SummaryConfidence < 0.85 {
			session.SummaryConfidence = 0.85
		}
		if strings.TrimSpace(session.SummarySource) == "" || strings.EqualFold(session.SummarySource, "fallback") {
			session.SummarySource = "human_review"
		}
		if note := normalizeCodexSummary(input.InternalMessageHR, 280); note != "" {
			session.InternalMessageHR = note
		} else {
			session.InternalMessageHR = normalizeCodexSummary(
				fmt.Sprintf("Odobreno za klijentsku sinkronizaciju: %s", candidate),
				280,
			)
		}
	case "edit":
		candidate := usefulCodexSummary(input.ClientMessageHR, 220)
		if candidate == "" || isGenericCodexClientMessage(candidate) {
			return nil, errors.New("edited client message is required")
		}
		session.ClientMessageHR = candidate
		session.SummaryHR = candidate
		session.ReviewStatus = codexReviewStatusApproved
		session.SummaryConfidence = 0.90
		session.SummarySource = "human_review"
		if note := normalizeCodexSummary(input.InternalMessageHR, 280); note != "" {
			session.InternalMessageHR = note
		} else {
			session.InternalMessageHR = normalizeCodexSummary(
				fmt.Sprintf("Ručno uređen sažetak: %s", candidate),
				280,
			)
		}
	case "reject":
		session.ClientMessageHR = ""
		session.ReviewStatus = codexReviewStatusRejected
		session.SummaryHR = "Codex aktivnost je odbijena za klijentsku sinkronizaciju."
		if note := normalizeCodexSummary(input.InternalMessageHR, 280); note != "" {
			session.InternalMessageHR = note
		} else {
			session.InternalMessageHR = "Odbijeno za klijentsku sinkronizaciju nakon ručnog pregleda."
		}
	case "internal":
		session.ClientMessageHR = ""
		session.ReviewStatus = codexReviewStatusInternalOnly
		session.SummaryHR = "Codex aktivnost je zadržana samo za internu evidenciju."
		if note := normalizeCodexSummary(input.InternalMessageHR, 280); note != "" {
			session.InternalMessageHR = note
		} else {
			session.InternalMessageHR = "Zadržano za internu evidenciju; bez klijentske sinkronizacije."
		}
	default:
		return nil, errors.New("unsupported review action")
	}

	if err := s.repository.Upsert(session); err != nil {
		return nil, err
	}
	return session, nil
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

	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.T().Before(sessions[j].StartedAt.T())
	})

	groups := make([][]*models.CodexTaskSession, 0)
	for _, session := range sessions {
		if !shouldIncludeCodexWorklogSession(session) {
			continue
		}
		if len(groups) == 0 {
			groups = append(groups, []*models.CodexTaskSession{session})
			continue
		}

		lastIndex := len(groups) - 1
		lastGroup := groups[lastIndex]
		lastSession := lastGroup[len(lastGroup)-1]
		if shouldSplitCodexWorklogGroup(lastSession, session) {
			groups = append(groups, []*models.CodexTaskSession{session})
			continue
		}

		groups[lastIndex] = append(lastGroup, session)
	}

	worklogs := make([]*CodexTaskWorklog, 0, len(groups))
	for _, group := range groups {
		worklogs = append(worklogs, buildCodexGroupedWorklog(codexTaskWorklogGroupedKey(group), group))
	}
	return worklogs, nil
}

func shouldIncludeCodexWorklogSession(session *models.CodexTaskSession) bool {
	if session == nil || session.EndedAt == nil {
		return false
	}
	if hasCodexSessionEvidence(session) {
		return true
	}
	if source := strings.TrimSpace(strings.ToLower(session.SummarySource)); source != "" && source != "fallback" {
		return true
	}
	return session.DurationSeconds >= codexMinNoEvidenceWorklogSeconds
}

func hasCodexSessionEvidence(session *models.CodexTaskSession) bool {
	note := strings.ToLower(session.TechnicalNote)
	if strings.Contains(note, "codex evidence:") && !strings.Contains(note, "no captured tool evidence") {
		return true
	}

	raw := strings.TrimSpace(session.EvidenceJSON)
	if raw == "" || raw == "null" || raw == "[]" || raw == "{}" {
		return false
	}

	var evidence []any
	if err := json.Unmarshal([]byte(raw), &evidence); err == nil {
		return len(evidence) > 0
	}

	var technicalEvidence struct {
		Events []any `json:"events"`
	}
	if err := json.Unmarshal([]byte(raw), &technicalEvidence); err == nil {
		return len(technicalEvidence.Events) > 0
	}

	return false
}

func buildCodexGroupedWorklog(key string, sessions []*models.CodexTaskSession) *CodexTaskWorklog {
	first := sessions[0]
	startedAt := first.StartedAt.T()
	endedAt := first.EndedAt.T()
	duration := 0.0
	workspaceRoot := first.WorkspaceRoot
	repository := first.Repository
	branch := first.Branch

	for _, session := range sessions {
		start := session.StartedAt.T()
		end := session.EndedAt.T()
		if start.Before(startedAt) {
			startedAt = start
		}
		if end.After(endedAt) {
			endedAt = end
		}
		duration += session.DurationSeconds
		workspaceRoot = lastNonEmptyString(workspaceRoot, session.WorkspaceRoot)
		repository = lastNonEmptyString(repository, session.Repository)
		branch = lastNonEmptyString(branch, session.Branch)
	}

	summaryOriginal := codexGroupSummaryField(sessions, func(session *models.CodexTaskSession) string {
		return session.SummaryHROriginal
	})
	summaryNormalized := codexGroupSummaryField(sessions, func(session *models.CodexTaskSession) string {
		return session.SummaryHRNormalized
	})
	clientMessage := codexGroupSummaryField(sessions, func(session *models.CodexTaskSession) string {
		return session.ClientMessageHR
	})
	if clientMessage == "" {
		clientMessage = buildCodexGroupedSummary(first.Project, sessions)
	}
	reviewStatus := codexGroupReviewStatus(sessions, clientMessage)
	if reviewStatus == "needs_review" {
		clientMessage = ""
	}

	summary := strings.TrimSpace(clientMessage)
	if summary == "" {
		summary = strings.TrimSpace(summaryNormalized)
	}
	if summary == "" {
		summary = strings.TrimSpace(summaryOriginal)
	}
	if summary == "" {
		summary = buildCodexGroupedSummary(first.Project, sessions)
	}
	if reviewStatus == "needs_review" && isGenericCodexClientMessage(summary) {
		summary = "Codex aktivnost zahtijeva ručni pregled."
	}

	return &CodexTaskWorklog{
		ID:                  "codex-chat-" + shortCodexHash(key),
		ExternalKey:         key,
		Project:             first.Project,
		Source:              models.CodexTaskWorklogSource,
		StartedAt:           startedAt,
		EndedAt:             endedAt,
		DurationSeconds:     duration,
		Summary:             summary,
		SummaryHROriginal:   summaryOriginal,
		SummaryHRNormalized: summaryNormalized,
		SummarySource:       codexGroupSummarySource(sessions),
		SummaryConfidence:   codexGroupSummaryConfidence(sessions),
		ClientMessageHR:     clientMessage,
		InternalMessageHR:   codexGroupInternalMessage(sessions, summaryNormalized, summaryOriginal),
		ReviewStatus:        reviewStatus,
		TechnicalNote:       buildCodexGroupedTechnicalNote(sessions),
		WorkspaceRoot:       workspaceRoot,
		Repository:          repository,
		Branch:              branch,
		Status:              models.CodexTaskSessionStatusClosed,
	}
}

func codexTaskWorklogGroupKey(session *models.CodexTaskSession) string {
	installation, chat := codexChatExternalKeyParts(session.ExternalKey)
	day := session.StartedAt.T().Format("20060102")
	project := safeCodexExternalKeyPart(session.Project, 64)
	return shortenCodexExternalKey(fmt.Sprintf("codex:chat:%s:%s:%s:%s", installation, chat, day, project), 240)
}

func shouldSplitCodexWorklogGroup(previous, current *models.CodexTaskSession) bool {
	if previous == nil || current == nil {
		return true
	}
	if codexTaskWorklogGroupKey(previous) != codexTaskWorklogGroupKey(current) {
		return true
	}

	previousEnd := previous.StartedAt.T()
	if previous.EndedAt != nil {
		previousEnd = previous.EndedAt.T()
	}
	currentStart := current.StartedAt.T()
	if currentStart.After(previousEnd) && currentStart.Sub(previousEnd) > codexGroupingGap {
		return true
	}

	return codexSessionObjectiveHash(previous) != codexSessionObjectiveHash(current)
}

func codexTaskWorklogGroupedKey(sessions []*models.CodexTaskSession) string {
	first := sessions[0]
	last := sessions[len(sessions)-1]
	baseKey := codexTaskWorklogGroupKey(first)
	objective := codexSessionObjectiveHash(first)
	entropy := shortCodexHash(fmt.Sprintf("%s|%s|%s", first.ExternalKey, last.ExternalKey, objective))
	return shortenCodexExternalKey(fmt.Sprintf("%s:%s", baseKey, entropy), 240)
}

func codexSessionObjectiveHash(session *models.CodexTaskSession) string {
	if session == nil {
		return "no-session"
	}

	evidence := parseCodexTechnicalEvidence(session.EvidenceJSON)
	parts := []string{
		strings.ToLower(strings.TrimSpace(session.Project)),
		strings.ToLower(strings.TrimSpace(session.Branch)),
		strings.ToLower(strings.TrimSpace(codexSessionTicketCandidate(session))),
		strings.ToLower(strings.TrimSpace(codexSessionIntentCategory(session, evidence))),
		strings.Join(codexSessionFileClusters(session, evidence), ","),
		strings.Join(evidence.SemanticEvidence, ","),
	}
	return shortCodexHash(strings.Join(parts, "|"))
}

func codexSessionTicketCandidate(session *models.CodexTaskSession) string {
	if session == nil {
		return ""
	}
	candidates := []string{
		session.Branch,
		session.Prompt,
		session.SummaryHR,
		session.SummaryHROriginal,
		session.SummaryHRNormalized,
		session.TechnicalNote,
		session.EvidenceJSON,
	}

	for _, candidate := range candidates {
		if match := codexTicketPattern.FindString(strings.ToUpper(candidate)); match != "" {
			return match
		}
	}

	return ""
}

func codexSessionIntentCategory(session *models.CodexTaskSession, evidence codexTechnicalEvidence) string {
	context := strings.ToLower(strings.Join([]string{
		session.SummarySource,
		session.SummaryHR,
		session.SummaryHROriginal,
		session.SummaryHRNormalized,
		session.Prompt,
		session.LastAssistantMessage,
		strings.Join(evidence.SemanticEvidence, " "),
	}, "\n"))

	if containsAnyCodexText(context, "deploy", "kubernetes", "helm", "terraform", "infra") {
		return "deploy_or_infra"
	}
	if containsAnyCodexText(context, "test", "pytest", "dotnet test", "go test") {
		return "test_run"
	}
	if containsAnyCodexText(context, "build", "compile", "bundle", "publish") {
		return "build_run"
	}
	if containsAnyCodexText(context, "database", "sql", "query", "mssql", "postgres") {
		return "database_query"
	}
	if containsAnyCodexText(context, "debug", "bug", "error", "review", "investigate", "analysis") {
		return "review_or_debugging"
	}
	if containsAnyCodexText(context, "plan", "design", "spec", "research") {
		return "planning_or_analysis"
	}
	if containsAnyCodexText(context, "patch", "update", "implement", "refactor", "code") {
		return "code_change"
	}
	return "unknown"
}

func codexSessionFileClusters(session *models.CodexTaskSession, evidence codexTechnicalEvidence) []string {
	paths := make([]string, 0)
	paths = append(paths, evidence.GitChangedFiles...)

	var explicitEvidence []string
	if err := json.Unmarshal([]byte(session.EvidenceJSON), &explicitEvidence); err == nil {
		for _, value := range explicitEvidence {
			if strings.HasPrefix(value, "command:") {
				continue
			}
			paths = append(paths, value)
		}
	}

	seen := map[string]bool{}
	clusters := make([]string, 0)
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		normalized := strings.TrimPrefix(strings.ReplaceAll(candidate, "\\", "/"), "./")
		cluster := normalized
		parts := strings.Split(normalized, "/")
		if len(parts) >= 2 {
			cluster = strings.Join(parts[:2], "/")
		}
		cluster = strings.ToLower(cluster)
		if cluster == "" || seen[cluster] {
			continue
		}
		seen[cluster] = true
		clusters = append(clusters, cluster)
		if len(clusters) == 5 {
			break
		}
	}
	sort.Strings(clusters)
	return clusters
}

type codexTechnicalEvidence struct {
	SemanticEvidence []string
	GitChangedFiles  []string
}

func parseCodexTechnicalEvidence(value string) codexTechnicalEvidence {
	if strings.TrimSpace(value) == "" {
		return codexTechnicalEvidence{}
	}

	var payload struct {
		SemanticEvidence []string `json:"semantic_evidence"`
		Git              struct {
			ChangedFiles []struct {
				Path string `json:"path"`
			} `json:"changed_files"`
		} `json:"git"`
	}
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return codexTechnicalEvidence{}
	}

	semanticEvidence := make([]string, 0, len(payload.SemanticEvidence))
	seenSemantic := map[string]bool{}
	for _, value := range payload.SemanticEvidence {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" || seenSemantic[value] {
			continue
		}
		seenSemantic[value] = true
		semanticEvidence = append(semanticEvidence, value)
	}

	gitChangedFiles := make([]string, 0, len(payload.Git.ChangedFiles))
	for _, file := range payload.Git.ChangedFiles {
		if path := strings.TrimSpace(file.Path); path != "" {
			gitChangedFiles = append(gitChangedFiles, path)
		}
	}

	return codexTechnicalEvidence{
		SemanticEvidence: semanticEvidence,
		GitChangedFiles:  gitChangedFiles,
	}
}

func codexChatExternalKeyParts(externalKey string) (string, string) {
	parts := strings.Split(strings.TrimSpace(externalKey), ":")
	if len(parts) >= 4 && strings.EqualFold(parts[0], "codex") {
		return safeCodexExternalKeyPart(parts[1], 64), safeCodexExternalKeyPart(parts[2], 96)
	}
	return "external", safeCodexExternalKeyPart(externalKey, 96)
}

func buildCodexGroupedSummary(project string, sessions []*models.CodexTaskSession) string {
	summaries := distinctCodexSessionSummaries(sessions)
	intentSummary := codexGroupedIntentSummary(project, sessions)
	humanSummaries := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if isLowValueCodexSummary(summary) {
			continue
		}
		humanSummaries = append(humanSummaries, summary)
	}
	if len(humanSummaries) > 0 {
		return buildCodexSummaryList(humanSummaries)
	}
	if intentSummary != "" {
		return intentSummary
	}
	if len(summaries) == 0 {
		project = strings.TrimSpace(project)
		if project == "" {
			return "Codex chat bez zabilježenog konteksta."
		}
		return normalizeCodexSummary(fmt.Sprintf("Codex chat na projektu %s bez zabilježenog konteksta.", project), 220)
	}

	return buildCodexSummaryList(summaries)
}

func buildCodexSummaryList(summaries []string) string {
	if len(summaries) == 1 {
		return summaries[0]
	}

	maxItems := len(summaries)
	if maxItems > 3 {
		maxItems = 3
	}
	for count := maxItems; count >= 1; count-- {
		parts := make([]string, 0, count+1)
		for _, summary := range summaries[:count] {
			parts = append(parts, trimCodexSentence(summary))
		}
		if remaining := len(summaries) - count; remaining > 0 {
			parts = append(parts, fmt.Sprintf("još %d aktivnosti", remaining))
		}
		candidate := ensureCodexSentence("Codex chat: " + strings.Join(parts, "; "))
		if len([]rune(candidate)) <= 220 {
			return candidate
		}
	}
	return normalizeCodexSummary("Codex chat: "+trimCodexSentence(summaries[0]), 220)
}

func codexGroupedIntentSummary(project string, sessions []*models.CodexTaskSession) string {
	parts := []string{project}
	hasSignal := false
	for _, session := range sessions {
		if hasCodexSessionEvidence(session) || isLowValueCodexSummary(session.SummaryHR) && session.SummaryHR != "" && session.SummaryHR != codexNoEvidenceSummary {
			hasSignal = true
		}
		parts = append(parts,
			session.Project,
			session.WorkspaceRoot,
			session.Repository,
			session.Branch,
			session.SummaryHR,
			session.Prompt,
			session.LastAssistantMessage,
			session.TechnicalNote,
			session.EvidenceJSON,
		)
	}
	if !hasSignal {
		return ""
	}
	return codexIntentSummary(project, parts...)
}

func isLowValueCodexSummary(summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" || summary == codexNoEvidenceSummary {
		return true
	}

	lower := strings.ToLower(summary)
	if strings.Contains(lower, "bez zabilježenog konteksta") || strings.Contains(lower, "bez zabiljezenog konteksta") {
		return true
	}
	if strings.HasPrefix(lower, "codex chat:") {
		rest := strings.TrimSpace(strings.TrimPrefix(lower, "codex chat:"))
		parts := strings.Split(rest, ";")
		lowValueParts := 0
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" || strings.HasPrefix(part, "još ") || strings.HasPrefix(part, "jos ") || isLowValueCodexSummary(part) {
				lowValueParts++
			}
		}
		return lowValueParts == len(parts)
	}
	if strings.HasPrefix(lower, "ažurirano ") || strings.HasPrefix(lower, "azurirano ") ||
		strings.HasPrefix(lower, "pregledano ") || strings.HasPrefix(lower, "provjereno stanje ") ||
		strings.HasPrefix(lower, "provjereni kubernetes ") || strings.HasPrefix(lower, "pokrenute projektne ") ||
		strings.HasPrefix(lower, "provjereno stanje repozitorija") {
		return true
	}
	return false
}

func codexIntentSummary(project string, parts ...string) string {
	context := strings.ToLower(strings.Join(parts, "\n"))
	project = strings.TrimSpace(project)
	projectLower := strings.ToLower(project)

	if containsAnyCodexText(context, "kubectl", "kubernetes", "fleet", "deployment", "helm", "ghcr.io", "rollout") &&
		!containsAnyCodexText(context, "codex_task", "codex task", "codex worklog", "codex-worklog") {
		return fmt.Sprintf("Rad na deployu i Kubernetes konfiguraciji projekta %s.", codexProjectLabel(project))
	}

	if containsAnyCodexText(context,
		"sqlcmd",
		"execute_sql",
		"mcp__mssql",
		"db_query",
		"database_query",
		"company_db_query",
		"select ",
	) {
		return fmt.Sprintf("Analiza podataka u bazi za projekt %s.", codexProjectLabel(project))
	}

	if containsAnyCodexText(context, "codex worklog", "codex-worklog", "codex_task", "codex task", "wakatime") ||
		strings.Contains(context, "wakapi") && containsAnyCodexText(context, "worklog", "wakatime", "codex_task", "codex task") {
		return "Rad na Codex worklog integraciji u Wakapiju."
	}

	if containsAnyCodexText(context,
		"delphi-decompiler",
		"delphi decompiler",
		"cli/check.sh",
		"decompiler",
		" idr",
	) {
		return "Rad na CLI provjerama i validaciji Delphi decompilera."
	}

	if projectLower == "ura" || containsAnyCodexText(context,
		"/ura/",
		"ura_",
		"onxpo",
	) {
		return "Rad na URA poslovnoj logici, testovima i migracijskim koracima."
	}

	if containsAnyCodexText(context,
		"onixphone",
		"document_batch",
		"pdf_batch",
		"batch_print",
		"dms ispis",
	) {
		return "Rad na OnixPhone DMS ispisu i obradi dokumenata."
	}

	if containsAnyCodexText(context,
		"test_",
		"_test.",
		"npm test",
		"yarn test",
		"go test",
		"dotnet test",
		"pytest",
	) {
		return fmt.Sprintf("Rad na testovima i provjerama projekta %s.", codexProjectLabel(project))
	}

	return ""
}

func containsAnyCodexText(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func codexProjectLabel(project string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		return "projekt"
	}
	return project
}

func distinctCodexSessionSummaries(sessions []*models.CodexTaskSession) []string {
	seen := map[string]bool{}
	result := make([]string, 0)
	for _, session := range sessions {
		summary := strings.TrimSpace(session.SummaryHR)
		if summary == "" || summary == codexNoEvidenceSummary {
			continue
		}
		key := strings.ToLower(summary)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, summary)
	}
	return result
}

func buildCodexGroupedTechnicalNote(sessions []*models.CodexTaskSession) string {
	if len(sessions) == 1 {
		return sessions[0].TechnicalNote
	}

	lines := []string{fmt.Sprintf("Grupirano %d Codex turna iz istog chata.", len(sessions))}
	for _, session := range sessions {
		if session.EndedAt == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s - %s, %ds): %s",
			session.ExternalKey,
			session.StartedAt.T().Format(time.RFC3339),
			session.EndedAt.T().Format(time.RFC3339),
			int(session.DurationSeconds),
			strings.TrimSpace(session.SummaryHR)))
		if note := strings.TrimSpace(session.TechnicalNote); note != "" {
			lines = append(lines, "  "+note)
		}
	}
	return strings.Join(lines, "\n")
}

func codexGroupSummaryField(sessions []*models.CodexTaskSession, getter func(*models.CodexTaskSession) string) string {
	for _, session := range sessions {
		if session == nil {
			continue
		}
		value := normalizeCodexSummary(getter(session), 220)
		if value != "" {
			return value
		}
	}
	return ""
}

func codexGroupSummarySource(sessions []*models.CodexTaskSession) string {
	source := ""
	for _, session := range sessions {
		current := strings.TrimSpace(strings.ToLower(session.SummarySource))
		if current == "" {
			continue
		}
		if source == "" {
			source = current
			continue
		}
		if source != current {
			return "grouped"
		}
	}
	if source == "" {
		return "fallback"
	}
	return source
}

func codexGroupSummaryConfidence(sessions []*models.CodexTaskSession) float64 {
	if len(sessions) == 0 {
		return 0
	}

	total := 0.0
	count := 0.0
	for _, session := range sessions {
		if session.SummaryConfidence <= 0 {
			continue
		}
		total += session.SummaryConfidence
		count++
	}
	if count == 0 {
		return 0.18
	}
	avg := total / count
	if avg > 1 {
		avg = 1
	}
	return avg
}

func codexGroupReviewStatus(sessions []*models.CodexTaskSession, summary string) string {
	if isGenericCodexClientMessage(summary) {
		return "needs_review"
	}
	for _, session := range sessions {
		status := strings.TrimSpace(strings.ToLower(session.ReviewStatus))
		if status == "needs_review" {
			return "needs_review"
		}
	}
	return "needs_grouping"
}

func codexGroupInternalMessage(sessions []*models.CodexTaskSession, normalized, original string) string {
	parts := make([]string, 0, len(sessions)+1)
	for _, session := range sessions {
		message := normalizeCodexSummary(session.InternalMessageHR, 220)
		if message == "" {
			continue
		}
		parts = append(parts, message)
		if len(parts) == 3 {
			break
		}
	}
	if len(parts) == 0 {
		if normalized != "" {
			return fmt.Sprintf("Predloženi sažetak: %s", normalized)
		}
		if original != "" {
			return fmt.Sprintf("Izvorni sažetak: %s", original)
		}
		return "Codex activity without enough evidence for client-facing summary."
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, " | ")
}

func codexSessionNeedsReview(session *models.CodexTaskSession) bool {
	if session == nil {
		return false
	}

	status := strings.TrimSpace(strings.ToLower(session.ReviewStatus))
	switch status {
	case codexReviewStatusApproved, codexReviewStatusAutoOK, codexReviewStatusRejected, codexReviewStatusInternalOnly:
		return false
	case codexReviewStatusNeedsReview, codexReviewStatusNeedsGrouping, codexReviewStatusPendingReview:
		return true
	}

	clientMessage := normalizeCodexSummary(session.ClientMessageHR, 220)
	if clientMessage == "" || isGenericCodexClientMessage(clientMessage) {
		return true
	}

	normalized := normalizeCodexSummary(session.SummaryHRNormalized, 220)
	if normalized == "" || isGenericCodexClientMessage(normalized) {
		return true
	}

	if session.SummaryConfidence <= 0 || session.SummaryConfidence < codexReviewQueueMinConfidence {
		return true
	}

	return false
}

func isGenericCodexClientMessage(summary string) bool {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return true
	}
	if strings.HasPrefix(strings.ToLower(summary), "codex chat:") {
		return true
	}
	if isLowValueCodexSummary(summary) {
		return true
	}
	return codexGenericClientPattern.MatchString(summary)
}

func safeCodexExternalKeyPart(value string, max int) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	previousDash := false
	for _, r := range value {
		allowed := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if allowed {
			b.WriteRune(r)
			previousDash = false
			continue
		}
		if !previousDash {
			b.WriteRune('-')
			previousDash = true
		}
	}
	cleaned := strings.Trim(b.String(), "-")
	if cleaned == "" {
		cleaned = "unknown"
	}
	if max > 0 && len(cleaned) > max {
		suffix := "-" + shortCodexHash(cleaned)
		limit := max - len(suffix)
		if limit < 1 {
			limit = max
			suffix = ""
		}
		cleaned = strings.Trim(cleaned[:limit], "-") + suffix
	}
	return cleaned
}

func shortenCodexExternalKey(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	suffix := ":" + shortCodexHash(value)
	limit := max - len(suffix)
	if limit < 1 {
		return shortCodexHash(value)
	}
	return strings.TrimRight(value[:limit], ":") + suffix
}

func shortCodexHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return fmt.Sprintf("%x", sum)[:12]
}

func trimCodexSentence(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), ".!?")
}

func lastNonEmptyString(current string, candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return current
	}
	return candidate
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

	summaryOriginal := usefulCodexSummary(input.SummaryHROriginal, 220)
	if summaryOriginal == "" {
		summaryOriginal = usefulCodexSummary(input.SummaryHR, 220)
	}
	if summaryOriginal == "" {
		summaryOriginal = buildCodexSummary(input)
	}

	summaryNormalized := usefulCodexSummary(input.SummaryHRNormalized, 220)
	if summaryNormalized == "" {
		summaryNormalized = usefulCodexSummary(input.SummaryHR, 220)
	}
	if summaryNormalized == "" {
		summaryNormalized = summaryOriginal
	}

	summarySource := strings.TrimSpace(strings.ToLower(input.SummarySource))
	if summarySource == "" {
		summarySource = "fallback"
	}
	summaryConfidence := input.SummaryConfidence
	if summaryConfidence <= 0 {
		switch summarySource {
		case "model":
			summaryConfidence = 0.72
		case "evidence":
			summaryConfidence = 0.66
		case "assistant":
			summaryConfidence = 0.46
		default:
			summaryConfidence = 0.18
		}
	}
	if summaryConfidence > 1 {
		summaryConfidence = 1
	}

	clientMessage := usefulCodexSummary(input.ClientMessageHR, 220)
	internalMessage := normalizeCodexSummary(input.InternalMessageHR, 280)
	reviewStatus := strings.TrimSpace(strings.ToLower(input.ReviewStatus))
	if reviewStatus == "" {
		if clientMessage == "" || summarySource == "fallback" || isGenericCodexClientMessage(summaryNormalized) {
			reviewStatus = "needs_review"
		} else {
			reviewStatus = "needs_grouping"
		}
	}
	if reviewStatus == "needs_review" {
		clientMessage = ""
	}

	summary := strings.TrimSpace(clientMessage)
	if summary == "" {
		summary = strings.TrimSpace(summaryNormalized)
	}
	if summary == "" {
		summary = strings.TrimSpace(summaryOriginal)
	}
	if summary == "" {
		summary = "Codex aktivnost zahtijeva ručni pregled."
	}
	if reviewStatus == "needs_review" && isGenericCodexClientMessage(summary) {
		summary = "Codex aktivnost zahtijeva ručni pregled."
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
		SummaryHROriginal:    summaryOriginal,
		SummaryHRNormalized:  summaryNormalized,
		SummarySource:        summarySource,
		SummaryConfidence:    summaryConfidence,
		ClientMessageHR:      clientMessage,
		InternalMessageHR:    internalMessage,
		ReviewStatus:         reviewStatus,
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

	if summary := codexNoToolIntentSummary(input); summary != "" {
		return summary
	}

	return "Codex aktivnost bez dovoljno konteksta za opis."
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

	if len(changedFiles) > 0 || len(inspectedFiles) > 0 || len(commands) > 0 {
		if summary := codexIntentSummary(input.Project,
			input.Project,
			input.WorkspaceRoot,
			input.Repository,
			input.Branch,
			input.Prompt,
			input.LastAssistantMessage,
			strings.Join(input.Evidence, "\n"),
			strings.Join(changedFiles, "\n"),
			strings.Join(inspectedFiles, "\n"),
			strings.Join(commands, "\n"),
			input.TechnicalEvidenceJSON,
		); summary != "" {
			return summary
		}
	}

	if len(changedFiles) > 0 {
		return codexFileSummary("Ažurirano", changedFiles, 1, 180)
	}
	if len(inspectedFiles) > 0 {
		return codexFileSummary("Pregledano", inspectedFiles, 2, 180)
	}
	if len(commands) > 0 {
		return codexCommandCategorySummary(commands)
	}
	return ""
}

func codexNoToolIntentSummary(input *CodexTaskSessionInput) string {
	project := codexProjectLabel(input.Project)
	context := strings.ToLower(cleanCodexSummaryText(strings.Join([]string{
		input.Prompt,
		input.LastAssistantMessage,
		input.SummaryHR,
		input.SummaryHROriginal,
		input.SummaryHRNormalized,
	}, "\n")))
	if context == "" {
		return ""
	}

	if containsAnyCodexText(context, "debug", "bug", "error", "stack trace", "failing test", "root cause", "problem") {
		return fmt.Sprintf("Analiza i otklanjanje problema na projektu %s.", project)
	}
	if containsAnyCodexText(context, "review", "code review", "pull request", "verify", "validation", "provjera", "pregled") {
		return fmt.Sprintf("Pregled i verifikacija rješenja za projekt %s.", project)
	}
	if containsAnyCodexText(context, "research", "investigate", "analysis", "analyse", "istraz", "analiz", "spike") {
		return fmt.Sprintf("Istraživanje i analiza zahtjeva za projekt %s.", project)
	}
	if containsAnyCodexText(context, "plan", "design", "spec", "architecture", "refactor", "implement", "milestone") {
		return fmt.Sprintf("Planiranje implementacije za projekt %s.", project)
	}

	return ""
}

func codexCommandCategorySummary(commands []string) string {
	joined := strings.ToLower(strings.Join(commands, "\n"))
	if regexp.MustCompile(`\bkubectl\b`).MatchString(joined) {
		return "Provjereni Kubernetes resursi."
	}
	if regexp.MustCompile(`\b(psql|sqlcmd|execute_sql|mcp__mssql)\b`).MatchString(joined) {
		return "Provjereno stanje baze podataka."
	}
	if regexp.MustCompile(`(?:db_query|database_query|company_db_query)`).MatchString(joined) {
		return "Provjereno stanje baze podataka."
	}
	if regexp.MustCompile(`\b(gh\s+(run|workflow|actions?)|git\s+)`).MatchString(joined) {
		return "Provjereno stanje repozitorija."
	}
	if regexp.MustCompile(`\b(npm|yarn|pnpm|dotnet|go)\s+(test|build|run)\b`).MatchString(joined) {
		return "Pokrenute projektne provjere."
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
		label = cleanFiles[0] + " i " + cleanFiles[1]
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
	if key == "" || codexFillerSummaries[key] || !isUsefulCodexWorkSummary(summary) || !isLikelyCroatianCodexSummary(summary) {
		return ""
	}
	return summary
}

func isLikelyCroatianCodexSummary(value string) bool {
	lower := strings.ToLower(value)
	for _, token := range codexCroatianTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
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
	if isLowValueCodexSummary(summary) {
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
