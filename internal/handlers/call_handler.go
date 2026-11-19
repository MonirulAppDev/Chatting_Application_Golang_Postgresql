// Package handlers defines the HTTP request handlers and controller logic for the application.
package handlers

import (
	"net/http"

	"backend/internal/models"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
CallHandler manages call history retrieval and related audio/video calling endpoints.
*/
type CallHandler struct {
	// Database connection pool for querying call history records
	DB *pgxpool.Pool
}

// NewCallHandler creates and returns a new CallHandler instance.
func NewCallHandler(db *pgxpool.Pool) *CallHandler {
	return &CallHandler{
		DB: db,
	}
}

// GetCallHistory retrieves call history logs for the authenticated user.
//
// @Summary Get call history
// @Description Fetch audio and video call logs involving the authenticated user (as caller or receiver), ordered chronologically descending by created_at.
// @Tags Calls
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "List of call log records"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/calls [get]
func (h *CallHandler) GetCallHistory(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract Authenticated User ID
	*/
	userID, ok := getUserIDFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: invalid or missing authentication credentials.",
		})
		return
	}

	/*
		Step 2: Query call_logs ordered by created_at DESC
	*/
	query := `
		SELECT 
			c.id,
			c.caller_id,
			c.receiver_id,
			c.call_type,
			c.status,
			c.duration_seconds,
			c.started_at,
			c.created_at,
			COALESCE(u1.username, ''),
			u1.avatar_url,
			COALESCE(u2.username, ''),
			u2.avatar_url
		FROM call_logs c
		LEFT JOIN users u1 ON c.caller_id = u1.id
		LEFT JOIN users u2 ON c.receiver_id = u2.id
		WHERE c.caller_id = $1 OR c.receiver_id = $1
		ORDER BY c.created_at DESC;
	`

	rows, err := h.DB.Query(r.Context(), query, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch call history records from database.",
		})
		return
	}
	defer rows.Close()

	calls := make([]models.CallLog, 0)
	for rows.Next() {
		var c models.CallLog
		if err := rows.Scan(
			&c.ID,
			&c.CallerID,
			&c.ReceiverID,
			&c.CallType,
			&c.Status,
			&c.DurationSeconds,
			&c.StartedAt,
			&c.CreatedAt,
			&c.CallerUsername,
			&c.CallerAvatarURL,
			&c.ReceiverUsername,
			&c.ReceiverAvatarURL,
		); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Error scanning call log records.",
			})
			return
		}
		calls = append(calls, c)
	}

	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Error iterating call log records.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"calls": calls,
		"count": len(calls),
	})
}
