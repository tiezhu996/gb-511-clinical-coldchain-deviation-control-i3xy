package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/repository"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

type SensorEvidenceService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.SensorEvidence], error)
	Get(context.Context, uint) (model.SensorEvidence, error)
	Create(context.Context, dto.CreateSensorEvidence, string, string) (model.SensorEvidence, error)
}

type sensorEvidenceService struct {
	repository repository.SensorEvidenceRepository
	minio      *minio.Client
	bucket     string
}

func NewSensorEvidenceService(repo repository.SensorEvidenceRepository, client *minio.Client, bucket string) SensorEvidenceService {
	return &sensorEvidenceService{repository: repo, minio: client, bucket: bucket}
}

func (s *sensorEvidenceService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.SensorEvidence], error) {
	return s.repository.List(ctx, query)
}
func (s *sensorEvidenceService) Get(ctx context.Context, id uint) (model.SensorEvidence, error) {
	return s.repository.Get(ctx, id)
}

func (s *sensorEvidenceService) Create(ctx context.Context, input dto.CreateSensorEvidence, actor, requestID string) (model.SensorEvidence, error) {
	if s.minio == nil || strings.TrimSpace(s.bucket) == "" {
		return model.SensorEvidence{}, fmt.Errorf("object storage is unavailable")
	}
	excursion, container := strings.ToUpper(strings.TrimSpace(input.ExcursionCode)), strings.ToUpper(strings.TrimSpace(input.ContainerCode))
	if strings.HasPrefix(input.ObjectKey, "/") || strings.Contains(input.ObjectKey, "..") {
		return model.SensorEvidence{}, fmt.Errorf("%w: unsafe evidence object key", ErrInvalidInput)
	}
	valid, err := s.repository.ReferencesExist(ctx, excursion, container)
	if err != nil || !valid {
		return model.SensorEvidence{}, fmt.Errorf("%w: evidence must reference an existing excursion and its container", ErrInvalidInput)
	}
	exists, err := s.minio.BucketExists(ctx, s.bucket)
	if err != nil {
		return model.SensorEvidence{}, fmt.Errorf("check evidence bucket: %w", err)
	}
	if !exists {
		if err := s.minio.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return model.SensorEvidence{}, fmt.Errorf("create evidence bucket: %w", err)
		}
	}
	item := model.SensorEvidence{Code: strings.ToUpper(strings.TrimSpace(input.Code)), ExcursionCode: excursion, ContainerCode: container, ObjectKey: strings.TrimSpace(input.ObjectKey), SHA256: strings.ToLower(input.SHA256), MediaType: strings.TrimSpace(input.MediaType), SizeBytes: input.SizeBytes, CapturedAt: input.CapturedAt.UTC(), CapturedBy: actor, Source: strings.TrimSpace(input.Source), CreatedAt: time.Now().UTC()}
	upload, err := s.minio.PresignedPutObject(ctx, s.bucket, item.ObjectKey, 15*time.Minute)
	if err != nil {
		return model.SensorEvidence{}, fmt.Errorf("sign evidence upload: %w", err)
	}
	audit := &model.AuditLog{Actor: actor, RequestID: requestID, Action: "evidence_registered", EntityType: "SensorEvidence", BeforeState: "", AfterState: "registered", Detail: fmt.Sprintf("object=%s sha256=%s excursion=%s", item.ObjectKey, item.SHA256, item.ExcursionCode), CreatedAt: time.Now().UTC()}
	if err := s.repository.Create(ctx, &item, audit); err != nil {
		return model.SensorEvidence{}, fmt.Errorf("register sensor evidence: %w", err)
	}
	item.UploadURL = upload.String()
	return item, nil
}

// Review block codes returned to callers and persisted on the excursion while it stays in_review.
const (
	ReviewBlockEvidenceMissing   = "EVIDENCE_MISSING"
	ReviewBlockContainerMismatch = "CONTAINER_MISMATCH"
	ReviewBlockDigestReused      = "DIGEST_REUSED"
)

// SnapshotConflict carries a machine-readable conflict code, a human-readable blocking reason and
// the offending evidence/snapshot code so callers can keep the excursion pending and display it.
type SnapshotConflict struct {
	Code        string
	Reason      string
	ConflictRef string
}

func (e *SnapshotConflict) Error() string { return e.Reason }

// PreparedSnapshot is the validated snapshot plus whether it must still be frozen on commit.
type PreparedSnapshot struct {
	Snapshot model.EvidenceReviewSnapshot
	IsNew    bool
}

type EvidenceReviewSnapshotService interface {
	List(context.Context, dto.PageQuery, string) (repository.Page[model.EvidenceReviewSnapshot], error)
	Get(context.Context, uint) (model.EvidenceReviewSnapshot, error)
	GetByExcursion(context.Context, string) (model.EvidenceReviewSnapshot, error)
	// PrepareEvaluation selects one registered evidence and validates it before the excursion is
	// decided, returning a *SnapshotConflict that must keep the excursion in_review.
	PrepareEvaluation(ctx context.Context, excursion model.ExcursionEvent, actor string) (PreparedSnapshot, error)
	CommitEvaluation(ctx context.Context, excursionID, expectedVersion uint, excursion *model.ExcursionEvent, snapshot *model.EvidenceReviewSnapshot, audit *model.AuditLog) error
	PersistReviewBlock(ctx context.Context, excursionID, expectedVersion uint, excursion *model.ExcursionEvent) error
}

type evidenceReviewSnapshotService struct {
	repository repository.EvidenceReviewSnapshotRepository
	evidence   repository.SensorEvidenceRepository
}

func NewEvidenceReviewSnapshotService(repo repository.EvidenceReviewSnapshotRepository, evidence repository.SensorEvidenceRepository) EvidenceReviewSnapshotService {
	return &evidenceReviewSnapshotService{repository: repo, evidence: evidence}
}

func (s *evidenceReviewSnapshotService) List(ctx context.Context, query dto.PageQuery, excursionCode string) (repository.Page[model.EvidenceReviewSnapshot], error) {
	return s.repository.List(ctx, query, excursionCode)
}

func (s *evidenceReviewSnapshotService) Get(ctx context.Context, id uint) (model.EvidenceReviewSnapshot, error) {
	return s.repository.Get(ctx, id)
}

func (s *evidenceReviewSnapshotService) GetByExcursion(ctx context.Context, code string) (model.EvidenceReviewSnapshot, error) {
	return s.repository.GetByExcursion(ctx, code)
}

func (s *evidenceReviewSnapshotService) PrepareEvaluation(ctx context.Context, excursion model.ExcursionEvent, actor string) (PreparedSnapshot, error) {
	// Idempotent reuse: an already decided excursion has an immutable snapshot; re-evaluation must
	// not create a second record or silently refreeze different evidence.
	if existing, err := s.repository.GetByExcursion(ctx, excursion.Code); err == nil {
		evidence, evidenceErr := s.evidence.LatestForExcursion(ctx, excursion.Code)
		if evidenceErr != nil {
			return PreparedSnapshot{}, conflict(ReviewBlockEvidenceMissing, "证据缺失：已登记证据不存在，无法复核快照", existing.EvidenceCode)
		}
		if !strings.EqualFold(evidence.ContainerCode, excursion.ContainerCode) {
			return PreparedSnapshot{}, conflict(ReviewBlockContainerMismatch,
				fmt.Sprintf("容器不一致：证据容器 %s 与偏差容器 %s 不符", evidence.ContainerCode, excursion.ContainerCode), evidence.Code)
		}
		if !strings.EqualFold(evidence.SHA256, existing.SHA256) {
			return PreparedSnapshot{}, conflict(ReviewBlockDigestReused, "证据摘要与已冻结复核快照不一致", existing.Code)
		}
		return PreparedSnapshot{Snapshot: existing, IsNew: false}, nil
	} else if err != gorm.ErrRecordNotFound {
		return PreparedSnapshot{}, fmt.Errorf("load evidence review snapshot: %w", err)
	}

	selected, err := s.evidence.LatestForExcursion(ctx, excursion.Code)
	if err == gorm.ErrRecordNotFound {
		return PreparedSnapshot{}, conflict(ReviewBlockEvidenceMissing,
			fmt.Sprintf("证据缺失：偏差 %s 尚无已登记的传感器证据", excursion.Code), excursion.Code)
	} else if err != nil {
		return PreparedSnapshot{}, fmt.Errorf("select registered evidence: %w", err)
	}
	if !strings.EqualFold(selected.ContainerCode, excursion.ContainerCode) {
		return PreparedSnapshot{}, conflict(ReviewBlockContainerMismatch,
			fmt.Sprintf("容器不一致：证据 %s 的容器 %s 与偏差容器 %s 不符", selected.Code, selected.ContainerCode, excursion.ContainerCode), selected.Code)
	}
	if holder, err := s.repository.GetByDigest(ctx, selected.SHA256); err == nil && !strings.EqualFold(holder.ExcursionCode, excursion.Code) {
		return PreparedSnapshot{}, conflict(ReviewBlockDigestReused,
			fmt.Sprintf("摘要重复：证据 %s 的 SHA-256 已被偏差 %s 的复核快照冻结", selected.Code, holder.ExcursionCode), holder.Code)
	} else if err != nil && err != gorm.ErrRecordNotFound {
		return PreparedSnapshot{}, fmt.Errorf("check frozen digest: %w", err)
	}

	return PreparedSnapshot{
		IsNew: true,
		Snapshot: model.EvidenceReviewSnapshot{
			Code:          snapshotCode(excursion.Code),
			ExcursionCode: excursion.Code,
			EvidenceCode:  selected.Code,
			SHA256:        strings.ToLower(selected.SHA256),
			ContainerCode: strings.ToUpper(selected.ContainerCode),
			CreatedAt:     time.Now().UTC(),
			CreatedBy:     actor,
		},
	}, nil
}

func (s *evidenceReviewSnapshotService) CommitEvaluation(ctx context.Context, excursionID, expectedVersion uint, excursion *model.ExcursionEvent, snapshot *model.EvidenceReviewSnapshot, audit *model.AuditLog) error {
	return s.repository.CommitEvaluation(ctx, excursionID, expectedVersion, excursion, snapshot, audit)
}

func (s *evidenceReviewSnapshotService) PersistReviewBlock(ctx context.Context, excursionID, expectedVersion uint, excursion *model.ExcursionEvent) error {
	return s.repository.PersistReviewBlock(ctx, excursionID, expectedVersion, excursion)
}

func snapshotCode(excursionCode string) string {
	return "SRS-" + strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(excursionCode)), "EE-")
}

func conflict(code, reason, ref string) *SnapshotConflict {
	return &SnapshotConflict{Code: code, Reason: reason, ConflictRef: ref}
}
