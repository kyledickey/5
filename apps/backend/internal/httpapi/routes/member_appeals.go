package routes

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// submitAppeal creates the member's single appeal for an eligible case.
// @Summary Submit a case appeal
// @Tags Appeals
// @Accept json
// @Produce json
// @Param caseID path string true "Case ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param appeal body quack.AppealSubmissionInput true "Appeal answers"
// @Security CookieAuth
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} apierror.Response
// @Failure 401 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Failure 409 {object} apierror.Response
// @Router /members/me/cases/{caseID}/appeal [post]
func submitAppeal(c *gin.Context, appeals *quack.AppealService) {
	session := middleware.GetAuthSession(c)
	if session == nil {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
		return
	}
	var input quack.AppealSubmissionInput
	if err := decodeStrictJSON(c, &input); err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid appeal payload")
		return
	}
	result, err := appeals.Submit(c.Request.Context(), c.Param("caseID"), session.DiscordUserID, input)
	if err != nil {
		writeAppealError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"appeal": result})
}

// getMemberAppeal returns an appeal only to the member who owns it.
// @Summary Get the current member's appeal
// @Tags Appeals
// @Produce json
// @Param appealID path string true "Appeal ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Router /members/me/appeals/{appealID} [get]
func getMemberAppeal(c *gin.Context, appeals *quack.AppealService) {
	session := middleware.GetAuthSession(c)
	if session == nil {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
		return
	}
	result, err := appeals.GetMember(c.Request.Context(), c.Param("appealID"), session.DiscordUserID)
	if err != nil {
		writeAppealError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"appeal": result})
}
