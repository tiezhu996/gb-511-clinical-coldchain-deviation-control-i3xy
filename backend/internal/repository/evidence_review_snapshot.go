package repository

import (
	"context"
	"strings"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"gorm.io/gorm"
)

// EvidenceReviewSnapshotRepository owns persistence for the frozen review snapshots.
type EvidenceReviewSnapshotRepository interface {
	Create(context.Context, *model.EvidenceReviewSnapshot) error
	GetForExcursion(context.Context, string) (model.EvidenceReviewSnapshot, error)
	ListForExcursions(context.Context, []string) ([]model.EvidenceReviewSnapshot, error)
	FindByDigest(context.Context, string) (model.EvidenceReviewSnapshot, error)
}

type evidenceReviewSnapshotRepository struct{ db *gorm.DB }

func NewEvidenceReviewSnapshotRepository(db *gorm.DB) EvidenceReviewSnapshotRepository {
	return &evidenceReviewSnapshotRepository{db: db}
}

func (r *evidenceReviewSnapshotRepository) Create(ctx context.Context, item *model.EvidenceReviewSnapshot) error {
	return r.db.WithContext(ctx).Create(item).Error
}

func (r *evidenceReviewSnapshotRepository) GetForExcursion(ctx context.Context, excursionCode string) (model.EvidenceReviewSnapshot, error) {
	var item model.EvidenceReviewSnapshot
	err := r.db.WithContext(ctx).Where("excursion_code = ?", strings.TrimSpace(excursionCode)).First(&item).Error
	return item, err
}

func (r *evidenceReviewSnapshotRepository) ListForExcursions(ctx context.Context, excursionCodes []string) ([]model.EvidenceReviewSnapshot, error) {
	items := make([]model.EvidenceReviewSnapshot, 0)
	if len(excursionCodes) == 0 {
		return items, nil
	}
	err := r.db.WithContext(ctx).Where("excursion_code IN ?", excursionCodes).Find(&items).Error
	return items, err
}

func (r *evidenceReviewSnapshotRepository) FindByDigest(ctx context.Context, digest string) (model.EvidenceReviewSnapshot, error) {
	var item model.EvidenceReviewSnapshot
	err := r.db.WithContext(ctx).Where("sha256 = ?", strings.ToLower(strings.TrimSpace(digest))).First(&item).Error
	return item, err
}
