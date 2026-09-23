package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/constants"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/repository"
	"gorm.io/gorm"
)

// SnapshotConflictError keeps an excursion in_review when the evidence review
// snapshot cannot be frozen. ConflictCode identifies the record that blocks the
// transition (the excursion, the mismatched evidence or the digest owner).
type SnapshotConflictError struct {
	Reason       string
	ConflictCode string
	Message      string
}

func (e *SnapshotConflictError) Error() string { return e.Message }

const (
	SnapshotConflictEvidenceMissing   = "evidence_missing"
	SnapshotConflictContainerMismatch = "container_mismatch"
	SnapshotConflictDigestDuplicate   = "digest_duplicate"
)

type ExcursionEventService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.ExcursionEvent], error)
	Get(context.Context, uint) (model.ExcursionEvent, error)
	Create(context.Context, dto.CreateExcursionEvent, string, string) (model.ExcursionEvent, error)
	Update(context.Context, uint, dto.UpdateExcursionEvent, string, string) (model.ExcursionEvent, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string) (model.ExcursionEvent, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type excursionEventService struct {
	repository  repository.ExcursionEventRepository
	disposition repository.DispositionDecisionRepository
	evidence    repository.SensorEvidenceRepository
	snapshots   repository.EvidenceReviewSnapshotRepository
	security    SecurityService
}

func NewExcursionEventService(repo repository.ExcursionEventRepository, disposition repository.DispositionDecisionRepository, evidence repository.SensorEvidenceRepository, snapshots repository.EvidenceReviewSnapshotRepository, security SecurityService) ExcursionEventService {
	return &excursionEventService{repository: repo, disposition: disposition, evidence: evidence, snapshots: snapshots, security: security}
}

func (s *excursionEventService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.ExcursionEvent], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	codes := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		codes = append(codes, item.Code)
	}
	snapshots, err := s.snapshots.ListForExcursions(ctx, codes)
	if err != nil {
		return page, err
	}
	byExcursion := make(map[string]model.EvidenceReviewSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byExcursion[snapshot.ExcursionCode] = snapshot
	}
	for i := range page.Items {
		applyReviewSnapshot(&page.Items[i], byExcursion[page.Items[i].Code])
	}
	return page, nil
}

func (s *excursionEventService) Get(ctx context.Context, id uint) (model.ExcursionEvent, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return item, err
	}
	if snapshot, err := s.snapshots.GetForExcursion(ctx, item.Code); err == nil {
		applyReviewSnapshot(&item, snapshot)
	}
	return item, nil
}

func applyReviewSnapshot(item *model.ExcursionEvent, snapshot model.EvidenceReviewSnapshot) {
	if snapshot.Code == "" {
		return
	}
	item.ReviewSnapshotCode = snapshot.Code
	item.ReviewSnapshotDigest = snapshot.SHA256
	item.ReviewSnapshotContainer = snapshot.ContainerCode
}

func (s *excursionEventService) Create(ctx context.Context, input dto.CreateExcursionEvent, actor, requestID string) (model.ExcursionEvent, error) {
	if err := validateExcursionEventBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ExcursionEvent{}, err
	}
	item := model.ExcursionEvent{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.ExcursionEventInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode:   strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
		ContainerCode: strings.ToUpper(strings.TrimSpace(firstNonEmpty(input.ContainerCode, input.RelatedCode))),
		WindowCode:    strings.ToUpper(strings.TrimSpace(input.WindowCode)),
		ObservedTempC: input.ObservedTempC, DurationMinutes: input.DurationMinutes,
		DetectedAt:     fallbackTime(input.DetectedAt, input.EffectiveAt),
		SensorEvidence: strings.TrimSpace(firstNonEmpty(input.SensorEvidence, input.Evidence)),
		Reviewer:       strings.TrimSpace(input.Reviewer),
	}
	if item.ObservedTempC == 0 {
		item.ObservedTempC = input.MetricValue
	}
	if item.ContainerCode == "" || item.WindowCode == "" || item.SensorEvidence == "" || item.DurationMinutes < 1 {
		return model.ExcursionEvent{}, fmt.Errorf("%w: container, temperature window, duration and sensor evidence are required", ErrInvalidInput)
	}
	if err := s.repository.Create(ctx, &item); err != nil {
		return model.ExcursionEvent{}, fmt.Errorf("create 偏差事件: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "ExcursionEvent", item.ID, "", item.Status, "created 偏差事件")
	return item, nil
}

func (s *excursionEventService) Update(ctx context.Context, id uint, input dto.UpdateExcursionEvent, actor, requestID string) (model.ExcursionEvent, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ExcursionEvent{}, err
	}
	if err := validateExcursionEventBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.ExcursionEvent{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.ContainerCode = strings.ToUpper(strings.TrimSpace(firstNonEmpty(input.ContainerCode, input.RelatedCode)))
	current.WindowCode = strings.ToUpper(strings.TrimSpace(input.WindowCode))
	current.ObservedTempC = input.ObservedTempC
	if current.ObservedTempC == 0 {
		current.ObservedTempC = input.MetricValue
	}
	current.DurationMinutes = input.DurationMinutes
	current.DetectedAt = fallbackTime(input.DetectedAt, input.EffectiveAt)
	current.SensorEvidence = strings.TrimSpace(firstNonEmpty(input.SensorEvidence, input.Evidence))
	current.Reviewer = strings.TrimSpace(input.Reviewer)
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.Update(ctx, id, input.ExpectedVersion, &current); err != nil {
		return model.ExcursionEvent{}, fmt.Errorf("update 偏差事件: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "ExcursionEvent", id, current.Status, current.Status, "updated business fields")
	return s.repository.Get(ctx, id)
}

func (s *excursionEventService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, requestID string) (model.ExcursionEvent, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.ExcursionEvent{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.ExcursionEventTransitions, current.Status, target) {
		return model.ExcursionEvent{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	before := current.Status
	evidence := strings.TrimSpace(input.Evidence)
	if evidence == "" {
		evidence = strings.TrimSpace(firstNonEmpty(current.SensorEvidence, current.Evidence))
	}
	if target == string(constants.ExcursionStateDecided) && evidence == "" {
		return model.ExcursionEvent{}, fmt.Errorf("%w: sensor evidence is required before deciding an excursion", ErrInvalidInput)
	}
	var snapshot *model.EvidenceReviewSnapshot
	if target == string(constants.ExcursionStateDecided) {
		frozen, conflict, err := s.prepareReviewSnapshot(ctx, &current, actor)
		if err != nil {
			return model.ExcursionEvent{}, err
		}
		if conflict != nil {
			// Keep the excursion in_review and persist the blocking reason so it
			// can be read back after a page refresh.
			_ = s.repository.SetReviewBlockReason(ctx, id, conflict.Message)
			return model.ExcursionEvent{}, conflict
		}
		snapshot = frozen
	}
	if target == string(constants.ExcursionStateClosed) {
		final, err := s.disposition.HasFinalForExcursion(ctx, current.Code)
		if err != nil || !final {
			return model.ExcursionEvent{}, fmt.Errorf("%w: a final disposition is required before closing an excursion", ErrInvalidInput)
		}
	}
	current.Status = target
	current.SensorEvidence = evidence
	current.Evidence = evidence
	if target == string(constants.ExcursionStateInReview) || target == string(constants.ExcursionStateDecided) {
		current.Reviewer = actor
	}
	if target == string(constants.ExcursionStateDecided) {
		current.ReviewBlockReason = ""
	}
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	detail, _ := json.Marshal(map[string]any{"reason": input.Reason, "sensorEvidence": evidence, "containerCode": current.ContainerCode, "reviewSnapshot": reviewSnapshotAudit(snapshot)})
	audit := auditLog(actor, requestID, "transition", "ExcursionEvent", id, before, target, string(detail))
	if snapshot != nil && snapshot.ID == 0 {
		if err := s.repository.UpdateWithSnapshot(ctx, id, input.ExpectedVersion, &current, snapshot, audit); err != nil {
			return model.ExcursionEvent{}, classifySnapshotWriteError(err, snapshot)
		}
	} else {
		if err := s.repository.Update(ctx, id, input.ExpectedVersion, &current, audit); err != nil {
			return model.ExcursionEvent{}, fmt.Errorf("transition 偏差事件: %w", err)
		}
	}
	return s.Get(ctx, id)
}

// prepareReviewSnapshot selects the newest registered sensor evidence for the
// excursion and freezes its code, SHA-256 and container. A nil snapshot with a
// nil error means an existing frozen snapshot is being reused.
func (s *excursionEventService) prepareReviewSnapshot(ctx context.Context, current *model.ExcursionEvent, actor string) (*model.EvidenceReviewSnapshot, *SnapshotConflictError, error) {
	if existing, err := s.snapshots.GetForExcursion(ctx, current.Code); err == nil {
		return &existing, nil, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, fmt.Errorf("load review snapshot: %w", err)
	}
	latest, err := s.evidence.LatestForExcursion(ctx, current.Code)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &SnapshotConflictError{
			Reason:       SnapshotConflictEvidenceMissing,
			ConflictCode: current.Code,
			Message:      fmt.Sprintf("证据缺失：偏差 %s 未登记传感器证据，保持待复核", current.Code),
		}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load sensor evidence: %w", err)
	}
	if !strings.EqualFold(latest.ContainerCode, current.ContainerCode) {
		return nil, &SnapshotConflictError{
			Reason:       SnapshotConflictContainerMismatch,
			ConflictCode: latest.Code,
			Message:      fmt.Sprintf("容器不一致：证据 %s 属于容器 %s，偏差容器为 %s，保持待复核", latest.Code, latest.ContainerCode, current.ContainerCode),
		}, nil
	}
	if owner, err := s.snapshots.FindByDigest(ctx, latest.SHA256); err == nil && owner.ExcursionCode != current.Code {
		return nil, &SnapshotConflictError{
			Reason:       SnapshotConflictDigestDuplicate,
			ConflictCode: owner.ExcursionCode,
			Message:      fmt.Sprintf("摘要重复：证据摘要 %s 已冻结给偏差 %s（快照 %s），保持待复核", shortDigest(latest.SHA256), owner.ExcursionCode, owner.Code),
		}, nil
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, fmt.Errorf("check snapshot digest: %w", err)
	}
	return &model.EvidenceReviewSnapshot{
		Code:          "RS-" + current.Code,
		ExcursionCode: current.Code,
		EvidenceCode:  latest.Code,
		SHA256:        strings.ToLower(strings.TrimSpace(latest.SHA256)),
		ContainerCode: latest.ContainerCode,
		CreatedBy:     actor,
		CreatedAt:     time.Now().UTC(),
	}, nil, nil
}

// classifySnapshotWriteError maps a failed atomic snapshot write to a conflict so
// concurrent decides cannot reuse one digest for two excursions.
func classifySnapshotWriteError(err error, snapshot *model.EvidenceReviewSnapshot) error {
	if errors.Is(err, repository.ErrVersionConflict) {
		return fmt.Errorf("transition 偏差事件: %w", err)
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "unique") || strings.Contains(lower, "duplicate") {
		return &SnapshotConflictError{
			Reason:       SnapshotConflictDigestDuplicate,
			ConflictCode: snapshot.ExcursionCode,
			Message:      fmt.Sprintf("摘要重复：证据摘要 %s 已被其他偏差冻结，保持待复核", shortDigest(snapshot.SHA256)),
		}
	}
	return fmt.Errorf("transition 偏差事件: %w", err)
}

func reviewSnapshotAudit(snapshot *model.EvidenceReviewSnapshot) map[string]any {
	if snapshot == nil {
		return nil
	}
	return map[string]any{
		"code": snapshot.Code, "evidenceCode": snapshot.EvidenceCode,
		"sha256": snapshot.SHA256, "containerCode": snapshot.ContainerCode,
	}
}

func shortDigest(digest string) string {
	digest = strings.TrimSpace(digest)
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

func (s *excursionEventService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "ExcursionEvent", id, current.Status, "deleted", "soft deleted 偏差事件")
}

func (s *excursionEventService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateExcursionEventBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
