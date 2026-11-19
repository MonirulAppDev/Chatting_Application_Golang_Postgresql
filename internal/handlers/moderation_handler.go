// Package handlers defines the HTTP request handlers and controller logic for the application.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"backend/internal/models"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
ModerationHandler manages moderation and safety operations including user blocking,
unblocking, and reporting users or chat messages.
*/
type ModerationHandler struct {
	// Database connection pool for executing moderation queries and transactions
	DB *pgxpool.Pool
}

// NewModerationHandler creates and returns a new ModerationHandler instance.
func NewModerationHandler(db *pgxpool.Pool) *ModerationHandler {
	return &ModerationHandler{
		DB: db,
	}
}

// BlockUser handles blocking another user and removing any existing friendship.
//
// @Summary Block a user
// @Description Block a user and permanently remove any active or pending friendship between both accounts
// @Tags Moderation
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body models.BlockUserRequest true "Target user ID to block"
// @Success 201 {object} map[string]interface{} "User blocked successfully"
// @Failure 400 {object} map[string]string "Invalid request or attempting to block self"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 {object} map[string]string "Target user not found"
// @Failure 409 {object} map[string]string "User is already blocked"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/users/block [post]
func (h *ModerationHandler) BlockUser(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract Authenticated Blocker User ID
	*/
	blockerID, ok := getUserIDFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: invalid or missing authentication credentials.",
		})
		return
	}

	/*
		Step 2: Parse Request Body
	*/
	var req models.BlockUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid request payload. 'blocked_id' is required.",
		})
		return
	}

	if req.BlockedID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid blocked_id provided.",
		})
		return
	}

	// Prevent user from blocking themselves
	if blockerID == req.BlockedID {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "You cannot block yourself.",
		})
		return
	}

	/*
		Step 3: Check if Target User Exists
	*/
	var userExists bool
	err := h.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE id = $1);", req.BlockedID).Scan(&userExists)
	if err != nil || !userExists {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "Target user to block does not exist.",
		})
		return
	}

	/*
		Step 4: Execute Block and Friendship Removal via Transaction
	*/
	tx, err := h.DB.Begin(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to start database transaction.",
		})
		return
	}
	defer tx.Rollback(r.Context())

	// Insert block record into blocks table
	insertBlockQuery := `
		INSERT INTO blocks (blocker_id, blocked_id)
		VALUES ($1, $2)
		RETURNING id, created_at;
	`
	var block models.Block
	block.BlockerID = blockerID
	block.BlockedID = req.BlockedID

	err = tx.QueryRow(r.Context(), insertBlockQuery, blockerID, req.BlockedID).Scan(
		&block.ID,
		&block.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "You have already blocked this user.",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Database error while creating block record.",
		})
		return
	}

	// Delete any existing friendship (pending, accepted, or rejected) between the two users
	deleteFriendshipQuery := `
		DELETE FROM friendships
		WHERE (sender_id = $1 AND receiver_id = $2)
		   OR (sender_id = $2 AND receiver_id = $1);
	`
	if _, err := tx.Exec(r.Context(), deleteFriendshipQuery, blockerID, req.BlockedID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to remove existing friendship records during block.",
		})
		return
	}

	// Commit transaction
	if err := tx.Commit(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to finalize block transaction.",
		})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"message": "User blocked successfully and friendship removed.",
		"block":   block,
	})
}

// UnblockUser handles removing an existing block between the authenticated user and another user.
//
// @Summary Unblock a user
// @Description Unblock a previously blocked user using their user ID via query parameter or JSON body
// @Tags Moderation
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param blocked_id query int false "Blocked user ID (query param)"
// @Param request body models.UnblockUserRequest false "Blocked user ID (JSON body)"
// @Success 200 {object} map[string]interface{} "User unblocked successfully"
// @Failure 400 {object} map[string]string "Invalid or missing blocked_id"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 {object} map[string]string "Block record not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/users/block [delete]
func (h *ModerationHandler) UnblockUser(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract Authenticated User ID
	*/
	blockerID, ok := getUserIDFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: invalid or missing authentication credentials.",
		})
		return
	}

	/*
		Step 2: Retrieve blocked_id from Query Parameter or JSON Body
	*/
	var blockedID int

	// First try reading from URL query parameter ?blocked_id=...
	queryVal := r.URL.Query().Get("blocked_id")
	if queryVal != "" {
		parsedID, err := strconv.Atoi(queryVal)
		if err == nil && parsedID > 0 {
			blockedID = parsedID
		}
	}

	// If not found in query parameter, try parsing request JSON body
	if blockedID == 0 && r.Body != nil {
		var req models.UnblockUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.BlockedID > 0 {
			blockedID = req.BlockedID
		}
	}

	// Validate blockedID
	if blockedID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Valid 'blocked_id' must be provided as a query parameter (?blocked_id=...) or JSON body.",
		})
		return
	}

	/*
		Step 3: Delete Record from blocks Table
	*/
	deleteQuery := `
		DELETE FROM blocks
		WHERE blocker_id = $1 AND blocked_id = $2;
	`
	result, err := h.DB.Exec(r.Context(), deleteQuery, blockerID, blockedID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Database error while unblocking user.",
		})
		return
	}

	if result.RowsAffected() == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "Block record not found: this user is not blocked.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":    "User unblocked successfully.",
		"blocked_id": blockedID,
	})
}

// Report handles submitting moderation reports for inappropriate users or messages.
//
// @Summary Report a user or message
// @Description Submit a safety and moderation report for a user or a specific message
// @Tags Moderation
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body models.ReportUserRequest true "Report payload with target user, optional message ID, and reason"
// @Success 201 {object} map[string]interface{} "Report submitted successfully"
// @Failure 400 {object} map[string]string "Invalid payload, reason empty, or reporting self"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 {object} map[string]string "Reported user or message not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/reports [post]
func (h *ModerationHandler) Report(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract Authenticated Reporter User ID
	*/
	reporterID, ok := getUserIDFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: invalid or missing authentication credentials.",
		})
		return
	}

	/*
		Step 2: Parse Request Body
	*/
	var req models.ReportUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid request payload. 'reported_user_id' and 'reason' are required.",
		})
		return
	}

	// Validate inputs
	if req.ReportedUserID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid reported_user_id provided.",
		})
		return
	}

	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Reason for the report cannot be empty.",
		})
		return
	}

	// Prevent reporter from reporting themselves
	if reporterID == req.ReportedUserID {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "You cannot report yourself.",
		})
		return
	}

	/*
		Step 3: Verify Reported User Exists
	*/
	var targetUserExists bool
	err := h.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE id = $1);", req.ReportedUserID).Scan(&targetUserExists)
	if err != nil || !targetUserExists {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "Reported user does not exist.",
		})
		return
	}

	/*
		Step 4: Verify Message Exists (if message_id provided)
	*/
	if req.MessageID != nil && *req.MessageID > 0 {
		var messageExists bool
		err := h.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1);", *req.MessageID).Scan(&messageExists)
		if err != nil || !messageExists {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "Reported message does not exist.",
			})
			return
		}
	}

	/*
		Step 5: Insert Report into reports Table
	*/
	insertReportQuery := `
		INSERT INTO reports (reporter_id, reported_user_id, message_id, reason, status)
		VALUES ($1, $2, $3, $4, 'pending')
		RETURNING id, created_at;
	`
	var report models.Report
	report.ReporterID = reporterID
	report.ReportedUserID = req.ReportedUserID
	report.MessageID = req.MessageID
	report.Reason = req.Reason
	report.Status = "pending"

	err = h.DB.QueryRow(r.Context(), insertReportQuery, reporterID, req.ReportedUserID, req.MessageID, req.Reason).Scan(
		&report.ID,
		&report.CreatedAt,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to submit moderation report.",
		})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"message": "Report submitted successfully. Our team will review it.",
		"report":  report,
	})
}
