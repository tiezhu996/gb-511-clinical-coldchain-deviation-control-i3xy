package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/config"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/model"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type testServices struct {
	db           *gorm.DB
	excursions   ExcursionEventService
	dispositions DispositionDecisionService
}

func newTestServices(t *testing.T) testServices {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "snapshot-test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Role{}, &model.User{}, &model.AuditLog{}, &model.SensorEvidence{},
		&model.TransportContainer{}, &model.TemperatureWindow{},
		&model.ExcursionEvent{}, &model.DispositionDecision{}, &model.EvidenceReviewSnapshot{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{AppName: "snapshot-test", JWTSecret: "0123456789abcdef"})
	excursionRepo := repository.NewExcursionEventRepository(db)
	dispositionRepo := repository.NewDispositionDecisionRepository(db)
	evidenceRepo := repository.NewSensorEvidenceRepository(db)
	snapshotRepo := repository.NewEvidenceReviewSnapshotRepository(db)
	return testServices{
		db:           db,
		excursions:   NewExcursionEventService(excursionRepo, dispositionRepo, evidenceRepo, snapshotRepo, security),
		dispositions: NewDispositionDecisionService(dispositionRepo, evidenceRepo, snapshotRepo, security),
	}
}

func createExcursion(t *testing.T, svc ExcursionEventService, code, container string) model.ExcursionEvent {
	t.Helper()
	item, err := svc.Create(context.Background(), dto.CreateExcursionEvent{
		Code: code, Name: code + " 温度偏差", Facility: "上海配送中心", Owner: "reviewer",
		Category: "高温偏差", RiskLevel: "high", MetricValue: 9.1, MetricUnit: "C",
		EffectiveAt: time.Now().UTC(), Evidence: "minio://sensor/trace.csv",
		ContainerCode: container, WindowCode: "TW-001", ObservedTempC: 9.1,
		DurationMinutes: 12, DetectedAt: time.Now().UTC(), SensorEvidence: "minio://sensor/trace.csv",
	}, "reviewer", "req-create")
	if err != nil {
		t.Fatalf("create excursion %s: %v", code, err)
	}
	item, err = svc.Transition(context.Background(), item.ID, dto.TransitionRequest{
		Status: "in_review", ExpectedVersion: item.Version, Reason: "接收复核",
	}, "reviewer", "req-review")
	if err != nil {
		t.Fatalf("move %s to in_review: %v", code, err)
	}
	return item
}

func registerEvidence(t *testing.T, db *gorm.DB, code, excursion, container, digest string) {
	t.Helper()
	row := model.SensorEvidence{
		Code: code, ExcursionCode: excursion, ContainerCode: container,
		ObjectKey: "sensor/" + strings.ToLower(code) + ".csv", SHA256: digest,
		MediaType: "text/csv", SizeBytes: 1024, CapturedAt: time.Now().UTC(),
		CapturedBy: "SN-TEST", Source: "calibrated-logger", CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("register evidence %s: %v", code, err)
	}
}

func decide(t *testing.T, svc ExcursionEventService, item model.ExcursionEvent) (model.ExcursionEvent, error) {
	t.Helper()
	return svc.Transition(context.Background(), item.ID, dto.TransitionRequest{
		Status: "decided", ExpectedVersion: item.Version, Reason: "影响评估完成",
	}, "reviewer", "req-decide")
}

func TestDecideFreezesEvidenceReviewSnapshot(t *testing.T) {
	svc := newTestServices(t)
	excursion := createExcursion(t, svc.excursions, "EE-T1", "TC-100")
	digest := strings.Repeat("a", 64)
	registerEvidence(t, svc.db, "SE-T1", "EE-T1", "TC-100", digest)

	decided, err := decide(t, svc.excursions, excursion)
	if err != nil {
		t.Fatalf("decide with registered evidence: %v", err)
	}
	if decided.Status != "decided" {
		t.Fatalf("expected decided, got %s", decided.Status)
	}
	if decided.ReviewSnapshotCode != "RS-EE-T1" {
		t.Fatalf("expected snapshot code RS-EE-T1, got %q", decided.ReviewSnapshotCode)
	}
	if decided.ReviewSnapshotDigest != digest || decided.ReviewSnapshotContainer != "TC-100" {
		t.Fatalf("snapshot projection mismatch: %+v", decided)
	}
	var snapshot model.EvidenceReviewSnapshot
	if err := svc.db.Where("excursion_code = ?", "EE-T1").First(&snapshot).Error; err != nil {
		t.Fatalf("snapshot row missing: %v", err)
	}
	if snapshot.EvidenceCode != "SE-T1" || snapshot.SHA256 != digest || snapshot.ContainerCode != "TC-100" {
		t.Fatalf("snapshot did not freeze evidence fields: %+v", snapshot)
	}
	reloaded, err := svc.excursions.Get(context.Background(), decided.ID)
	if err != nil {
		t.Fatalf("reload excursion: %v", err)
	}
	if reloaded.ReviewSnapshotCode != "RS-EE-T1" || reloaded.ReviewSnapshotDigest != digest {
		t.Fatalf("snapshot must be readable after refresh: %+v", reloaded)
	}

	// Re-reviewing and deciding again reuses the frozen snapshot instead of duplicating it.
	reopened, err := svc.excursions.Transition(context.Background(), decided.ID, dto.TransitionRequest{
		Status: "in_review", ExpectedVersion: decided.Version, Reason: "补充复核",
	}, "reviewer", "req-reopen")
	if err != nil {
		t.Fatalf("reopen review: %v", err)
	}
	if _, err := decide(t, svc.excursions, reopened); err != nil {
		t.Fatalf("decide again with frozen snapshot: %v", err)
	}
	var snapshots int64
	if err := svc.db.Model(&model.EvidenceReviewSnapshot{}).Where("excursion_code = ?", "EE-T1").Count(&snapshots).Error; err != nil || snapshots != 1 {
		t.Fatalf("expected exactly one snapshot for EE-T1, got %d (err=%v)", snapshots, err)
	}
}

func TestDigestCannotBeReusedAcrossExcursions(t *testing.T) {
	svc := newTestServices(t)
	digest := strings.Repeat("b", 64)
	first := createExcursion(t, svc.excursions, "EE-T2A", "TC-210")
	registerEvidence(t, svc.db, "SE-T2A", "EE-T2A", "TC-210", digest)
	if _, err := decide(t, svc.excursions, first); err != nil {
		t.Fatalf("decide first excursion: %v", err)
	}

	second := createExcursion(t, svc.excursions, "EE-T2B", "TC-211")
	registerEvidence(t, svc.db, "SE-T2B", "EE-T2B", "TC-211", digest)
	_, err := decide(t, svc.excursions, second)
	var conflict *SnapshotConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected snapshot conflict, got %v", err)
	}
	if conflict.Reason != SnapshotConflictDigestDuplicate || conflict.ConflictCode != "EE-T2A" {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}
	reloaded, getErr := svc.excursions.Get(context.Background(), second.ID)
	if getErr != nil {
		t.Fatalf("reload blocked excursion: %v", getErr)
	}
	if reloaded.Status != "in_review" {
		t.Fatalf("blocked excursion must stay in_review, got %s", reloaded.Status)
	}
	if !strings.Contains(reloaded.ReviewBlockReason, "EE-T2A") {
		t.Fatalf("block reason must reference the conflicting excursion, got %q", reloaded.ReviewBlockReason)
	}
}

func TestMissingEvidenceKeepsExcursionInReview(t *testing.T) {
	svc := newTestServices(t)
	excursion := createExcursion(t, svc.excursions, "EE-T3", "TC-300")
	_, err := decide(t, svc.excursions, excursion)
	var conflict *SnapshotConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected snapshot conflict, got %v", err)
	}
	if conflict.Reason != SnapshotConflictEvidenceMissing || conflict.ConflictCode != "EE-T3" {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}
	reloaded, _ := svc.excursions.Get(context.Background(), excursion.ID)
	if reloaded.Status != "in_review" || !strings.Contains(reloaded.ReviewBlockReason, "证据缺失") {
		t.Fatalf("expected persisted missing-evidence block, got status=%s reason=%q", reloaded.Status, reloaded.ReviewBlockReason)
	}
}

func TestContainerMismatchKeepsExcursionInReview(t *testing.T) {
	svc := newTestServices(t)
	excursion := createExcursion(t, svc.excursions, "EE-T4", "TC-400")
	registerEvidence(t, svc.db, "SE-T4", "EE-T4", "TC-999", strings.Repeat("d", 64))
	_, err := decide(t, svc.excursions, excursion)
	var conflict *SnapshotConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected snapshot conflict, got %v", err)
	}
	if conflict.Reason != SnapshotConflictContainerMismatch || conflict.ConflictCode != "SE-T4" {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}
	reloaded, _ := svc.excursions.Get(context.Background(), excursion.ID)
	if reloaded.Status != "in_review" || !strings.Contains(reloaded.ReviewBlockReason, "容器不一致") {
		t.Fatalf("expected persisted container-mismatch block, got status=%s reason=%q", reloaded.Status, reloaded.ReviewBlockReason)
	}
}

func TestDispositionValidatesReviewSnapshotDigest(t *testing.T) {
	svc := newTestServices(t)
	excursion := createExcursion(t, svc.excursions, "EE-T5", "TC-500")
	registerEvidence(t, svc.db, "SE-T5", "EE-T5", "TC-500", strings.Repeat("e", 64))

	proposal := dto.CreateDispositionDecision{
		Code: "DD-T5", Name: "EE-T5 放行提议", Facility: "质量放行组", Owner: "operator",
		Category: "放行", RiskLevel: "high", MetricValue: 9.1, MetricUnit: "C",
		EffectiveAt: time.Now().UTC(), Evidence: "minio://sensor/trace.csv",
		ExcursionCode: "EE-T5", DecisionBasis: "稳定性评估支持放行", SensorEvidence: "minio://sensor/trace.csv",
	}
	if _, err := svc.dispositions.Create(context.Background(), proposal, "operator", "req-propose"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("proposal without review snapshot must be rejected, got %v", err)
	}

	decided, err := decide(t, svc.excursions, excursion)
	if err != nil {
		t.Fatalf("decide excursion: %v", err)
	}
	if decided.ReviewSnapshotCode == "" {
		t.Fatal("decided excursion must carry a review snapshot")
	}

	created, err := svc.dispositions.Create(context.Background(), proposal, "operator", "req-propose")
	if err != nil {
		t.Fatalf("proposal with frozen snapshot: %v", err)
	}
	if _, err := svc.dispositions.Transition(context.Background(), created.ID, dto.TransitionRequest{
		Status: "release", ExpectedVersion: created.Version, Reason: "独立复核通过",
	}, "reviewer", "req-approve"); err != nil {
		t.Fatalf("approval against frozen snapshot: %v", err)
	}

	// If the frozen digest no longer matches registered evidence, approval is blocked.
	if err := svc.db.Model(&model.SensorEvidence{}).Where("code = ?", "SE-T5").Update("sha256", strings.Repeat("f", 64)).Error; err != nil {
		t.Fatalf("tamper evidence digest: %v", err)
	}
	second := dto.CreateDispositionDecision{
		Code: "DD-T5B", Name: "EE-T5 二次提议", Facility: "质量放行组", Owner: "operator",
		Category: "放行", RiskLevel: "high", MetricValue: 9.1, MetricUnit: "C",
		EffectiveAt: time.Now().UTC(), Evidence: "minio://sensor/trace.csv",
		ExcursionCode: "EE-T5", DecisionBasis: "再次评估", SensorEvidence: "minio://sensor/trace.csv",
	}
	if _, err := svc.dispositions.Create(context.Background(), second, "operator", "req-propose-2"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("digest mismatch must block disposition creation, got %v", err)
	}
}
