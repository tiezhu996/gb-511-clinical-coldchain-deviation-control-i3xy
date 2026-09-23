package repository

import (
	"context"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"gorm.io/gorm"
)

// ExcursionEventRepository owns all persistence operations for 偏差事件.
type ExcursionEventRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.ExcursionEvent], error)
	Get(context.Context, uint) (model.ExcursionEvent, error)
	Create(context.Context, *model.ExcursionEvent) error
	Update(context.Context, uint, uint, *model.ExcursionEvent, ...*model.AuditLog) error
	UpdateWithSnapshot(context.Context, uint, uint, *model.ExcursionEvent, *model.EvidenceReviewSnapshot, *model.AuditLog) error
	SetReviewBlockReason(context.Context, uint, string) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type excursionEventRepository struct {
	store *Store[model.ExcursionEvent]
	db    *gorm.DB
}

func NewExcursionEventRepository(db *gorm.DB) ExcursionEventRepository {
	return &excursionEventRepository{store: NewStore[model.ExcursionEvent](db), db: db}
}

func (r *excursionEventRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.ExcursionEvent], error) {
	return r.store.List(ctx, q)
}
func (r *excursionEventRepository) Get(ctx context.Context, id uint) (model.ExcursionEvent, error) {
	return r.store.Get(ctx, id)
}
func (r *excursionEventRepository) Create(ctx context.Context, item *model.ExcursionEvent) error {
	return r.store.Create(ctx, item)
}
func (r *excursionEventRepository) Update(ctx context.Context, id, version uint, item *model.ExcursionEvent, audits ...*model.AuditLog) error {
	return r.store.Update(ctx, id, version, item, audits...)
}

// UpdateWithSnapshot freezes the review snapshot and moves the excursion in one
// transaction so a decided excursion always has its snapshot and vice versa.
func (r *excursionEventRepository) UpdateWithSnapshot(ctx context.Context, id, version uint, item *model.ExcursionEvent, snapshot *model.EvidenceReviewSnapshot, audit *model.AuditLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if snapshot != nil {
			if err := tx.Create(snapshot).Error; err != nil {
				return err
			}
		}
		return NewStore[model.ExcursionEvent](tx).Update(ctx, id, version, item, audit)
	})
}

// SetReviewBlockReason records why a decide attempt was blocked without touching
// the optimistic-lock version, so a blocked transition never invalidates the copy
// a reviewer is looking at.
func (r *excursionEventRepository) SetReviewBlockReason(ctx context.Context, id uint, reason string) error {
	return r.db.WithContext(ctx).Model(&model.ExcursionEvent{}).Where("id = ?", id).UpdateColumn("review_block_reason", reason).Error
}

func (r *excursionEventRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *excursionEventRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
