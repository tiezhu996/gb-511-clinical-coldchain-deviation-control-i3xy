package service

import (
	"context"
	"errors"
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

func snapshotTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_pragma=busy_timeout(5000)"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&model.SensorEvidence{}, &model.EvidenceReviewSnapshot{},
		&model.ExcursionEvent{}, &model.DispositionDecision{}, &model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func newSnapshotFixture(t *testing.T) (*gorm.DB, EvidenceReviewSnapshotService, repository.ExcursionEventRepository, repository.DispositionDecisionRepository) {
	db := snapshotTestDB(t)
	evidenceRepo := repository.NewSensorEvidenceRepository(db)
	snapshotRepo := repository.NewEvidenceReviewSnapshotRepository(db)
	excursionRepo := repository.NewExcursionEventRepository(db)
	dispositionRepo := repository.NewDispositionDecisionRepository(db)
	snapshotService := NewEvidenceReviewSnapshotService(snapshotRepo, evidenceRepo)
	return db, snapshotService, excursionRepo, dispositionRepo
}

func insertEvidence(t *testing.T, db *gorm.DB, code, excursion, container, sha string) {
	t.Helper()
	if err := db.Create(&model.SensorEvidence{
		Code: code, ExcursionCode: excursion, ContainerCode: container,
		ObjectKey: "sensor/" + strings.ToLower(code) + "/trace.csv", SHA256: sha,
		MediaType: "text/csv", SizeBytes: 1024, CapturedAt: time.Now().UTC(),
		CapturedBy: "logger", Source: "calibrated-logger", CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatalf("insert evidence: %v", err)
	}
}

func insertExcursion(t *testing.T, db *gorm.DB, code, container, status string, version uint) model.ExcursionEvent {
	t.Helper()
	item := model.ExcursionEvent{
		BaseModel: model.BaseModel{
			Code: code, Name: "偏差 " + code, Status: status, Version: version,
		},
		ContainerCode: container,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("insert excursion: %v", err)
	}
	return item
}

func asConflict(t *testing.T, err error) *SnapshotConflict {
	t.Helper()
	var conflict *SnapshotConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("expected SnapshotConflict, got %v", err)
	}
	return conflict
}

func TestPrepareEvaluationMissingEvidence(t *testing.T) {
	env := newSnapEnv(t)
	excursion := insertExcursion(t, env.db, "EE-NOE", "TC-X", "in_review", 1)
	_, err := env.snapshots.PrepareEvaluation(context.Background(), excursion, "reviewer")
	conflict := asConflict(t, err)
	if conflict.Code != ReviewBlockEvidenceMissing {
		t.Fatalf("expected %s, got %s", ReviewBlockEvidenceMissing, conflict.Code)
	}
	if !strings.Contains(conflict.Reason, "证据缺失") {
		t.Fatalf("expected evidence-missing reason, got %q", conflict.Reason)
	}
}

type snapEnv struct {
	db              *gorm.DB
	snapshots       EvidenceReviewSnapshotService
	excursionRepo   repository.ExcursionEventRepository
	dispositionRepo repository.DispositionDecisionRepository
	excursionSvc    ExcursionEventService
	dispositionSvc  DispositionDecisionService
}

func newSnapEnv(t *testing.T) snapEnv {
	db, snapshots, excursionRepo, dispositionRepo := newSnapshotFixture(t)
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{AppName: "test", JWTSecret: "test-secret-123456"})
	excursionSvc := NewExcursionEventService(excursionRepo, dispositionRepo, snapshots, security)
	dispositionSvc := NewDispositionDecisionService(dispositionRepo, snapshots, security)
	return snapEnv{db: db, snapshots: snapshots, excursionRepo: excursionRepo, dispositionRepo: dispositionRepo,
		excursionSvc: excursionSvc, dispositionSvc: dispositionSvc}
}

func TestPrepareEvaluationContainerMismatchConflict(t *testing.T) {
	env := newSnapEnv(t)
	excursion := insertExcursion(t, env.db, "EE-MIS", "TC-EXPECTED", "in_review", 1)
	insertEvidence(t, env.db, "SE-MIS", "EE-MIS", "TC-OTHER", strings.Repeat("a", 64))
	_, err := env.snapshots.PrepareEvaluation(context.Background(), excursion, "reviewer")
	conflict := asConflict(t, err)
	if conflict.Code != ReviewBlockContainerMismatch {
		t.Fatalf("expected %s, got %s", ReviewBlockContainerMismatch, conflict.Code)
	}
	if conflict.ConflictRef != "SE-MIS" {
		t.Fatalf("expected conflicting evidence code SE-MIS, got %q", conflict.ConflictRef)
	}
}

func TestPrepareEvaluationDigestReusedConflict(t *testing.T) {
	env := newSnapEnv(t)
	// First excursion freezes a snapshot with the a-digest.
	first := insertExcursion(t, env.db, "EE-A", "TC-A", "in_review", 1)
	insertEvidence(t, env.db, "SE-A", "EE-A", "TC-A", strings.Repeat("a", 64))
	prepared, err := env.snapshots.PrepareEvaluation(context.Background(), first, "reviewer")
	if err != nil {
		t.Fatalf("first freeze failed: %v", err)
	}
	if !prepared.IsNew || prepared.Snapshot.SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("expected new snapshot freezing the a-digest")
	}
	if err := env.db.Create(&prepared.Snapshot).Error; err != nil {
		t.Fatalf("persist first snapshot: %v", err)
	}

	// A second excursion registers evidence carrying the same SHA-256: must be rejected as reused.
	second := insertExcursion(t, env.db, "EE-B", "TC-B", "in_review", 1)
	insertEvidence(t, env.db, "SE-B", "EE-B", "TC-B", strings.Repeat("a", 64))
	_, err = env.snapshots.PrepareEvaluation(context.Background(), second, "reviewer")
	conflict := asConflict(t, err)
	if conflict.Code != ReviewBlockDigestReused {
		t.Fatalf("expected %s, got %s", ReviewBlockDigestReused, conflict.Code)
	}
	if !strings.Contains(conflict.ConflictRef, "SRS-") {
		t.Fatalf("expected conflict reference to be the existing snapshot code, got %q", conflict.ConflictRef)
	}
}

func TestPrepareEvaluationFreezesChosenEvidence(t *testing.T) {
	env := newSnapEnv(t)
	excursion := insertExcursion(t, env.db, "EE-OK", "TC-OK", "in_review", 1)
	insertEvidence(t, env.db, "SE-OLD", "EE-OK", "TC-OK", strings.Repeat("1", 64))
	insertEvidence(t, env.db, "SE-NEW", "EE-OK", "TC-OK", strings.Repeat("2", 64))
	prepared, err := env.snapshots.PrepareEvaluation(context.Background(), excursion, "reviewer")
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if !prepared.IsNew {
		t.Fatal("expected a new snapshot")
	}
	if prepared.Snapshot.EvidenceCode != "SE-NEW" {
		t.Fatalf("expected the latest evidence SE-NEW to be frozen, got %q", prepared.Snapshot.EvidenceCode)
	}
	if prepared.Snapshot.Code != "SRS-OK" || prepared.Snapshot.ExcursionCode != "EE-OK" ||
		prepared.Snapshot.ContainerCode != "TC-OK" || prepared.Snapshot.CreatedBy != "reviewer" {
		t.Fatalf("snapshot fields incorrect: %+v", prepared.Snapshot)
	}
}

func TestPrepareEvaluationIdempotentReplay(t *testing.T) {
	env := newSnapEnv(t)
	excursion := insertExcursion(t, env.db, "EE-IDEM", "TC-IDEM", "decided", 2)
	insertEvidence(t, env.db, "SE-IDEM", "EE-IDEM", "TC-IDEM", strings.Repeat("d", 64))
	existing := model.EvidenceReviewSnapshot{
		Code: "SRS-IDEM", ExcursionCode: "EE-IDEM", EvidenceCode: "SE-IDEM",
		SHA256: strings.Repeat("d", 64), ContainerCode: "TC-IDEM", CreatedBy: "reviewer", CreatedAt: time.Now().UTC(),
	}
	if err := env.db.Create(&existing).Error; err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	prepared, err := env.snapshots.PrepareEvaluation(context.Background(), excursion, "reviewer")
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if prepared.IsNew || prepared.Snapshot.Code != "SRS-IDEM" {
		t.Fatal("expected the existing immutable snapshot to be reused")
	}
}

func TestTransitionStaysInReviewAndPersistsBlockThenSucceeds(t *testing.T) {
	env := newSnapEnv(t)
	// No evidence: attempting to decide must keep the excursion in_review and record the block.
	excursion := insertExcursion(t, env.db, "EE-BLOCK", "TC-BLOCK", "in_review", 1)
	_, err := env.excursionSvc.Transition(context.Background(), excursion.ID, dto.TransitionRequest{
		Status: "decided", ExpectedVersion: 1, Reason: "尝试完成影响评估",
	}, "reviewer", "req-1")
	conflict := asConflict(t, err)
	if conflict.Code != ReviewBlockEvidenceMissing {
		t.Fatalf("expected evidence missing block, got %s", conflict.Code)
	}
	stored, err := env.excursionRepo.Get(context.Background(), excursion.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Status != "in_review" {
		t.Fatalf("excursion must stay in_review, got %s", stored.Status)
	}
	if stored.ReviewBlockCode != ReviewBlockEvidenceMissing || stored.ReviewBlockReason == "" || stored.ReviewConflictRef != "EE-BLOCK" {
		t.Fatalf("block fields not persisted: %+v", stored)
	}
	if stored.Version != 2 {
		t.Fatalf("expected version bump to 2 after block, got %d", stored.Version)
	}

	// Register evidence and retry: snapshot is frozen and the excursion becomes decided.
	insertEvidence(t, env.db, "SE-BLOCK", "EE-BLOCK", "TC-BLOCK", strings.Repeat("e", 64))
	decided, err := env.excursionSvc.Transition(context.Background(), excursion.ID, dto.TransitionRequest{
		Status: "decided", ExpectedVersion: 2, Reason: "证据完整，评估完成",
	}, "reviewer", "req-2")
	if err != nil {
		t.Fatalf("decide failed: %v", err)
	}
	if decided.Status != "decided" || decided.SnapshotCode != "SRS-BLOCK" {
		t.Fatalf("expected decided with frozen snapshot, got status=%s snapshot=%s", decided.Status, decided.SnapshotCode)
	}
	if decided.ReviewBlockCode != "" {
		t.Fatalf("block fields must be cleared on success, got %q", decided.ReviewBlockCode)
	}
	snapshot, err := env.snapshots.GetByExcursion(context.Background(), "EE-BLOCK")
	if err != nil {
		t.Fatalf("snapshot readback failed: %v", err)
	}
	if snapshot.EvidenceCode != "SE-BLOCK" || snapshot.SHA256 != strings.Repeat("e", 64) || snapshot.ContainerCode != "TC-BLOCK" {
		t.Fatalf("frozen snapshot mismatch: %+v", snapshot)
	}
}

func dispositionCreateInput(code string) dto.CreateDispositionDecision {
	now := time.Now().UTC()
	return dto.CreateDispositionDecision{
		Code: code, Name: "处置 " + code, Facility: "质量放行组", Owner: "operator",
		Category: "隔离提议", RiskLevel: "high", EffectiveAt: now,
		ExcursionCode: "EE-BLOCK", SensorEvidence: "minio://trace.csv",
	}
}

func TestDispositionCreateRequiresSnapshot(t *testing.T) {
	env := newSnapEnv(t)
	// Excursion exists but has no frozen snapshot -> creation blocked.
	insertExcursion(t, env.db, "EE-BLOCK", "TC-BLOCK", "in_review", 1)
	_, err := env.dispositionSvc.Create(context.Background(), dispositionCreateInput("DD-NO"), "operator", "req-d1")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput without snapshot, got %v", err)
	}
}

func TestDispositionCreateAndApproveVerifySnapshotDigest(t *testing.T) {
	env := newSnapEnv(t)
	// Drive the excursion all the way to decided so its snapshot is frozen.
	excursion := insertExcursion(t, env.db, "EE-BLOCK", "TC-BLOCK", "in_review", 1)
	insertEvidence(t, env.db, "SE-BLOCK", "EE-BLOCK", "TC-BLOCK", strings.Repeat("e", 64))
	if _, err := env.excursionSvc.Transition(context.Background(), excursion.ID, dto.TransitionRequest{
		Status: "decided", ExpectedVersion: 1, Reason: "评估完成",
	}, "reviewer", "req-eval"); err != nil {
		t.Fatalf("decide excursion: %v", err)
	}

	// Creating a disposition binds the frozen digest automatically.
	created, err := env.dispositionSvc.Create(context.Background(), dispositionCreateInput("DD-OK"), "operator", "req-d2")
	if err != nil {
		t.Fatalf("create disposition: %v", err)
	}
	if created.SnapshotSHA256 != strings.Repeat("e", 64) || created.SnapshotEvidenceCode != "SE-BLOCK" {
		t.Fatalf("frozen digest not bound: %+v", created)
	}

	// Supplying a mismatched digest on creation must be rejected.
	bad := dispositionCreateInput("DD-BAD")
	bad.SnapshotSHA256 = strings.Repeat("f", 64)
	if _, err := env.dispositionSvc.Create(context.Background(), bad, "operator", "req-d3"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected mismatched digest rejection, got %v", err)
	}

	// A different reviewer approves; the digest is re-verified against the frozen snapshot.
	approved, err := env.dispositionSvc.Transition(context.Background(), created.ID, dto.TransitionRequest{
		Status: "quarantine", ExpectedVersion: 1, Reason: "证据复核通过，维持隔离",
	}, "reviewer", "req-d4")
	if err != nil {
		t.Fatalf("approve disposition: %v", err)
	}
	if approved.Status != "quarantine" || approved.ApprovedBy != "reviewer" {
		t.Fatalf("unexpected approved decision: %+v", approved)
	}
}
