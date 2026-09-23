package model

import "time"

// ExcursionEvent models 偏差事件 as an independently versioned aggregate. The fields
// cover ownership, operational context, evidence and measured risk so later
// changes naturally span persistence, service and UI layers.
type ExcursionEvent struct {
	BaseModel
	ContainerCode   string    `json:"containerCode" gorm:"size:64;index"`
	WindowCode      string    `json:"windowCode" gorm:"size:64;index"`
	ObservedTempC   float64   `json:"observedTempC"`
	DurationMinutes int       `json:"durationMinutes"`
	DetectedAt      time.Time `json:"detectedAt" gorm:"index"`
	SensorEvidence  string    `json:"sensorEvidence" gorm:"size:2000"`
	Reviewer        string    `json:"reviewer" gorm:"size:80"`
	Facility        string    `json:"facility" gorm:"size:120;index"`
	Owner           string    `json:"owner" gorm:"size:120;index"`
	Category        string    `json:"category" gorm:"size:80;index"`
	RiskLevel       string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" gorm:"size:24"`
	EffectiveAt     time.Time `json:"effectiveAt"`
	Evidence        string    `json:"evidence" gorm:"size:2000"`
	RelatedCode     string    `json:"relatedCode" gorm:"size:64;index"`
	// Review snapshot fields: SnapshotCode references the frozen evidence review snapshot once
	// the excursion is decided; ReviewBlock* persist the latest conflict while it stays in_review.
	SnapshotCode      string `json:"snapshotCode" gorm:"size:64;index"`
	ReviewBlockCode   string `json:"reviewBlockCode" gorm:"size:40"`
	ReviewBlockReason string `json:"reviewBlockReason" gorm:"size:500"`
	ReviewConflictRef string `json:"reviewConflictRef" gorm:"size:128"`
}

func (item *ExcursionEvent) GetBase() *BaseModel { return &item.BaseModel }

func (item ExcursionEvent) TableName() string { return "excursion_events" }

var ExcursionEventInitialStatus = "open"
