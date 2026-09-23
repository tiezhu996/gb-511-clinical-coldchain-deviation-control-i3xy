package handler

import (
	"net/http"

	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/dto"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/middleware"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/service"
	"github.com/blueship581/clinical-coldchain-deviation-control/backend/internal/util"
	"github.com/gin-gonic/gin"
)

type SensorEvidenceHandler struct{ service service.SensorEvidenceService }

func NewSensorEvidenceHandler(value service.SensorEvidenceService) *SensorEvidenceHandler {
	return &SensorEvidenceHandler{service: value}
}

func (h *SensorEvidenceHandler) Register(group *gin.RouterGroup) {
	resource := group.Group("/evidence")
	resource.GET("", h.list)
	resource.GET("/:id", h.get)
	resource.POST("", middleware.RequireMinimumRole("operator"), h.create)
}

func (h *SensorEvidenceHandler) list(c *gin.Context) {
	result, err := h.service.List(c.Request.Context(), bindPage(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.Page(c, result.Items, result.Page, result.PageSize, result.Total)
}
func (h *SensorEvidenceHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}
func (h *SensorEvidenceHandler) create(c *gin.Context) {
	var input dto.CreateSensorEvidence
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Create(c.Request.Context(), input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.Created(c, item)
}

// EvidenceReviewSnapshotHandler exposes the immutable review snapshots. Snapshots are
// system-generated when an excursion is decided, so only read routes are offered.
type EvidenceReviewSnapshotHandler struct {
	service service.EvidenceReviewSnapshotService
}

func NewEvidenceReviewSnapshotHandler(value service.EvidenceReviewSnapshotService) *EvidenceReviewSnapshotHandler {
	return &EvidenceReviewSnapshotHandler{service: value}
}

func (h *EvidenceReviewSnapshotHandler) Register(group *gin.RouterGroup) {
	resource := group.Group("/evidence-snapshots")
	resource.GET("", h.list)
	resource.GET("/:id", h.get)
}

func (h *EvidenceReviewSnapshotHandler) list(c *gin.Context) {
	page := bindPage(c)
	result, err := h.service.List(c.Request.Context(), dto.PageQuery{
		Page: page.Page, PageSize: page.PageSize, Search: c.Query("search"),
	}, c.Query("excursionCode"))
	if err != nil {
		handleError(c, err)
		return
	}
	util.Page(c, result.Items, result.Page, result.PageSize, result.Total)
}

func (h *EvidenceReviewSnapshotHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}
