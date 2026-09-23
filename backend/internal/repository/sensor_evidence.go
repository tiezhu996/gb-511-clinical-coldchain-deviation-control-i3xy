package repository

import (
	"context"
	"strings"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"gorm.io/gorm"
)

type SensorEvidenceRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.SensorEvidence], error)
	Get(context.Context, uint) (model.SensorEvidence, error)
	Create(context.Context, *model.SensorEvidence, *model.AuditLog) error
	CountForExcursion(context.Context, string) (int64, error)
	LatestForExcursion(context.Context, string) (model.SensorEvidence, error)
	ReferencesExist(context.Context, string, string) (bool, error)
}

type sensorEvidenceRepository struct{ db *gorm.DB }

func NewSensorEvidenceRepository(db *gorm.DB) SensorEvidenceRepository {
	return &sensorEvidenceRepository{db: db}
}

func (r *sensorEvidenceRepository) List(ctx context.Context, query dto.PageQuery) (Page[model.SensorEvidence], error) {
	page, pageSize := normalizePage(query.Page, query.PageSize)
	db := r.db.WithContext(ctx).Model(&model.SensorEvidence{})
	if search := strings.TrimSpace(query.Search); search != "" {
		like := "%" + search + "%"
		db = db.Where("code ILIKE ? OR excursion_code ILIKE ? OR container_code ILIKE ?", like, like, like)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return Page[model.SensorEvidence]{}, err
	}
	items := make([]model.SensorEvidence, 0)
	err := db.Order("captured_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return Page[model.SensorEvidence]{Items: items, Total: total, Page: page, PageSize: pageSize}, err
}

func (r *sensorEvidenceRepository) Get(ctx context.Context, id uint) (model.SensorEvidence, error) {
	var item model.SensorEvidence
	err := r.db.WithContext(ctx).First(&item, id).Error
	return item, err
}

func (r *sensorEvidenceRepository) Create(ctx context.Context, item *model.SensorEvidence, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		audit.EntityID = item.ID
		return tx.Create(audit).Error
	})
}

func (r *sensorEvidenceRepository) CountForExcursion(ctx context.Context, code string) (int64, error) {
	var total int64
	return total, r.db.WithContext(ctx).Model(&model.SensorEvidence{}).Where("excursion_code = ?", code).Count(&total).Error
}

// LatestForExcursion deterministically selects one registered evidence record (the most recently
// captured) so the review snapshot freezes a stable artifact rather than relying on caller input.
func (r *sensorEvidenceRepository) LatestForExcursion(ctx context.Context, code string) (model.SensorEvidence, error) {
	var item model.SensorEvidence
	err := r.db.WithContext(ctx).Where("excursion_code = ?", code).
		Order("captured_at DESC, id DESC").First(&item).Error
	return item, err
}

func (r *sensorEvidenceRepository) ReferencesExist(ctx context.Context, excursion, container string) (bool, error) {
	var excursions, containers int64
	if err := r.db.WithContext(ctx).Model(&model.ExcursionEvent{}).Where("code = ? AND container_code = ?", excursion, container).Count(&excursions).Error; err != nil {
		return false, err
	}
	if err := r.db.WithContext(ctx).Model(&model.TransportContainer{}).Where("code = ?", container).Count(&containers).Error; err != nil {
		return false, err
	}
	return excursions == 1 && containers == 1, nil
}

// ErrSnapshotDigestReused signals that a frozen SHA-256 is already claimed by another excursion's snapshot.
var ErrSnapshotDigestReused = errSnapshotDigestReused{}

type errSnapshotDigestReused struct{}

func (errSnapshotDigestReused) Error() string {
	return "evidence snapshot digest is already frozen for another excursion"
}

// IsDuplicateKey reports whether err is a database unique-constraint violation. SQLite,
// PostgreSQL and MySQL all surface "duplicate" / "unique" in the driver message.
func IsDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate") || strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicated key")
}

// EvidenceReviewSnapshotRepository owns persistence of the immutable cold-chain evidence review
// snapshot plus the two atomic transitions that couple it with 偏差事件.
type EvidenceReviewSnapshotRepository interface {
	List(context.Context, dto.PageQuery, string) (Page[model.EvidenceReviewSnapshot], error)
	Get(context.Context, uint) (model.EvidenceReviewSnapshot, error)
	GetByExcursion(context.Context, string) (model.EvidenceReviewSnapshot, error)
	GetByDigest(context.Context, string) (model.EvidenceReviewSnapshot, error)
	// CommitEvaluation freezes a new snapshot and moves the excursion in_review -> decided in one
	// transaction, guarded by the excursion optimistic version.
	CommitEvaluation(ctx context.Context, excursionID, expectedVersion uint, excursion *model.ExcursionEvent, snapshot *model.EvidenceReviewSnapshot, audit *model.AuditLog) error
	// PersistReviewBlock records a snapshot conflict on the excursion while leaving it in_review.
	PersistReviewBlock(ctx context.Context, excursionID, expectedVersion uint, excursion *model.ExcursionEvent) error
}

type evidenceReviewSnapshotRepository struct{ db *gorm.DB }

func NewEvidenceReviewSnapshotRepository(db *gorm.DB) EvidenceReviewSnapshotRepository {
	return &evidenceReviewSnapshotRepository{db: db}
}

func (r *evidenceReviewSnapshotRepository) List(ctx context.Context, query dto.PageQuery, excursionCode string) (Page[model.EvidenceReviewSnapshot], error) {
	page, pageSize := normalizePage(query.Page, query.PageSize)
	db := r.db.WithContext(ctx).Model(&model.EvidenceReviewSnapshot{})
	if code := strings.ToUpper(strings.TrimSpace(excursionCode)); code != "" {
		db = db.Where("excursion_code = ?", code)
	}
	if search := strings.TrimSpace(query.Search); search != "" {
		like := "%" + search + "%"
		db = db.Where("code ILIKE ? OR evidence_code ILIKE ? OR excursion_code ILIKE ?", like, like, like)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return Page[model.EvidenceReviewSnapshot]{}, err
	}
	items := make([]model.EvidenceReviewSnapshot, 0)
	err := db.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return Page[model.EvidenceReviewSnapshot]{Items: items, Total: total, Page: page, PageSize: pageSize}, err
}

func (r *evidenceReviewSnapshotRepository) Get(ctx context.Context, id uint) (model.EvidenceReviewSnapshot, error) {
	var item model.EvidenceReviewSnapshot
	err := r.db.WithContext(ctx).First(&item, id).Error
	return item, err
}

func (r *evidenceReviewSnapshotRepository) GetByExcursion(ctx context.Context, code string) (model.EvidenceReviewSnapshot, error) {
	var item model.EvidenceReviewSnapshot
	err := r.db.WithContext(ctx).Where("excursion_code = ?", strings.ToUpper(strings.TrimSpace(code))).First(&item).Error
	return item, err
}

func (r *evidenceReviewSnapshotRepository) GetByDigest(ctx context.Context, sha string) (model.EvidenceReviewSnapshot, error) {
	var item model.EvidenceReviewSnapshot
	err := r.db.WithContext(ctx).Where("sha256 = ?", strings.ToLower(strings.TrimSpace(sha))).First(&item).Error
	return item, err
}

func (r *evidenceReviewSnapshotRepository) CommitEvaluation(ctx context.Context, excursionID, expectedVersion uint, excursion *model.ExcursionEvent, snapshot *model.EvidenceReviewSnapshot, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if snapshot != nil && snapshot.Code != "" {
			if err := tx.Create(snapshot).Error; err != nil {
				if IsDuplicateKey(err) {
					return ErrSnapshotDigestReused
				}
				return err
			}
		}
		result := tx.Model(&model.ExcursionEvent{}).
			Where("id = ? AND version = ?", excursionID, expectedVersion).
			Select("*").Omit("id", "code", "created_at", "deleted_at").Updates(excursion)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		if audit != nil {
			if err := tx.Create(audit).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *evidenceReviewSnapshotRepository) PersistReviewBlock(ctx context.Context, excursionID, expectedVersion uint, excursion *model.ExcursionEvent) error {
	result := r.db.WithContext(ctx).Model(&model.ExcursionEvent{}).
		Where("id = ? AND version = ? AND status = ?", excursionID, expectedVersion, "in_review").
		Updates(map[string]any{
			"review_block_code":   excursion.ReviewBlockCode,
			"review_block_reason": excursion.ReviewBlockReason,
			"review_conflict_ref": excursion.ReviewConflictRef,
			"version":             gorm.Expr("version + 1"),
			"updated_at":          time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVersionConflict
	}
	return nil
}
