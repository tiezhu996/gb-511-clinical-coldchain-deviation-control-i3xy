package model

import "time"

// EvidenceReviewSnapshot freezes one registered sensor evidence (code, SHA-256 and
// container) at the moment an excursion moves from in_review to decided. The digest
// is unique across snapshots so the same evidence summary can never back two
// different excursions.
type EvidenceReviewSnapshot struct {
	ID            uint      `json:"id" gorm:"primaryKey"`
	Code          string    `json:"code" gorm:"size:64;uniqueIndex;not null"`
	ExcursionCode string    `json:"excursionCode" gorm:"size:64;uniqueIndex;not null"`
	EvidenceCode  string    `json:"evidenceCode" gorm:"size:64;not null"`
	SHA256        string    `json:"sha256" gorm:"size:64;uniqueIndex;not null"`
	ContainerCode string    `json:"containerCode" gorm:"size:64;not null"`
	CreatedBy     string    `json:"createdBy" gorm:"size:80;not null"`
	CreatedAt     time.Time `json:"createdAt" gorm:"index"`
}

func (EvidenceReviewSnapshot) TableName() string { return "evidence_review_snapshots" }
