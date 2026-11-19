// Package handlers defines the HTTP request handlers and controller logic for the application.
package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"backend/internal/models"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
ChatHandler manages chat message history retrieval and related messaging operations.
*/
type ChatHandler struct {
	// Database connection pool for querying message history
	DB *pgxpool.Pool
}

// NewChatHandler creates and returns a new ChatHandler instance.
func NewChatHandler(db *pgxpool.Pool) *ChatHandler {
	return &ChatHandler{
		DB: db,
	}
}

// GetMessages retrieves historical messages between the authenticated user and another user.
//
// @Summary Get chat history with a user
// @Description Fetch all historical messages exchanged between the authenticated user and target other_user_id ordered chronologically ascending.
// @Tags Messages
// @Security BearerAuth
// @Produce json
// @Param with query int true "Other User ID to fetch chat history with"
// @Success 200 {object} map[string]interface{} "List of chat messages"
// @Failure 400 {object} map[string]string "Invalid or missing 'with' parameter"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/messages [get]
func (h *ChatHandler) GetMessages(w http.ResponseWriter, r *http.Request) {
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
		Step 2: Parse 'with' Query Parameter
	*/
	withParam := r.URL.Query().Get("with")
	if withParam == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Missing 'with' query parameter. Example: /api/messages?with=2",
		})
		return
	}

	otherUserID, err := strconv.Atoi(withParam)
	if err != nil || otherUserID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Invalid 'with' user ID parameter provided.",
		})
		return
	}

	/*
		Step 3: Query Message History Ordered Chronologically (ASC)
	*/
	query := `
		SELECT 
			id, 
			sender_id, 
			receiver_id, 
			message_type, 
			content, 
			metadata, 
			is_read, 
			created_at
		FROM messages
		WHERE (sender_id = $1 AND receiver_id = $2)
		   OR (sender_id = $2 AND receiver_id = $1)
		ORDER BY created_at ASC;
	`

	rows, err := h.DB.Query(r.Context(), query, userID, otherUserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Database error while fetching message history.",
		})
		return
	}
	defer rows.Close()

	messages := make([]models.Message, 0)
	for rows.Next() {
		var msg models.Message
		var metadataBytes []byte

		if err := rows.Scan(
			&msg.ID,
			&msg.SenderID,
			&msg.ReceiverID,
			&msg.MessageType,
			&msg.Content,
			&metadataBytes,
			&msg.IsRead,
			&msg.CreatedAt,
		); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Error parsing message records from database.",
			})
			return
		}

		if len(metadataBytes) > 0 {
			msg.Metadata = json.RawMessage(metadataBytes)
		}

		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Error iterating through message records.",
		})
		return
	}

	/*
		Step 4: Return JSON Response
	*/
	writeJSON(w, http.StatusOK, map[string]any{
		"messages": messages,
		"count":    len(messages),
	})
}

// GetUnreadNotifications retrieves all unread notifications (is_read = false) for the authenticated user.
//
// @Summary Get unread notifications
// @Description Fetch all unread notifications for the authenticated user from the notifications table ordered descending.
// @Tags Notifications
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "List of unread notifications"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/notifications [get]
func (h *ChatHandler) GetUnreadNotifications(w http.ResponseWriter, r *http.Request) {
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
		Step 2: Query Unread Notifications from Database
	*/
	query := `
		SELECT 
			id, 
			user_id, 
			sender_id, 
			type, 
			title, 
			body, 
			is_read, 
			created_at
		FROM notifications
		WHERE user_id = $1 AND is_read = FALSE
		ORDER BY created_at DESC;
	`

	rows, err := h.DB.Query(r.Context(), query, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Database error while fetching notifications.",
		})
		return
	}
	defer rows.Close()

	notifications := make([]models.Notification, 0)
	for rows.Next() {
		var n models.Notification
		if err := rows.Scan(
			&n.ID,
			&n.UserID,
			&n.SenderID,
			&n.Type,
			&n.Title,
			&n.Body,
			&n.IsRead,
			&n.CreatedAt,
		); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Error parsing notification records from database.",
			})
			return
		}
		notifications = append(notifications, n)
	}

	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Error iterating through notification records.",
		})
		return
	}

	/*
		Step 3: Return JSON Response
	*/
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": notifications,
		"count":         len(notifications),
	})
}

// MarkNotificationsAsRead updates unread notifications (is_read = false) to is_read = true.
// If specific notification_ids or notification_id is provided, updates those;
// otherwise marks all unread notifications for the user as read.
//
// @Summary Mark notifications as read
// @Description Update notifications status to is_read = true for the authenticated user. If request body is empty or notification IDs omitted, marks all unread notifications as read.
// @Tags Notifications
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param request body models.MarkNotificationsReadRequest false "Optional notification IDs to mark as read"
// @Success 200 {object} map[string]interface{} "Notifications marked as read"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/notifications/read [post]
func (h *ChatHandler) MarkNotificationsAsRead(w http.ResponseWriter, r *http.Request) {
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
		Step 2: Parse Optional Request Body
	*/
	var req models.MarkNotificationsReadRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	// Consolidate target IDs if provided
	var targetIDs []int
	if req.NotificationID != nil && *req.NotificationID > 0 {
		targetIDs = append(targetIDs, *req.NotificationID)
	}
	for _, id := range req.NotificationIDs {
		if id > 0 {
			targetIDs = append(targetIDs, id)
		}
	}

	/*
		Step 3: Execute Database Update
	*/
	var rowsAffected int64

	if len(targetIDs) > 0 {
		// Update specific target notification IDs owned by this user
		updateQuery := `
			UPDATE notifications
			SET is_read = TRUE
			WHERE user_id = $1 AND id = ANY($2) AND is_read = FALSE;
		`
		res, err := h.DB.Exec(r.Context(), updateQuery, userID, targetIDs)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Database error updating notifications status.",
			})
			return
		}
		rowsAffected = res.RowsAffected()
	} else {
		// Mark all unread notifications for this user as read
		updateQuery := `
			UPDATE notifications
			SET is_read = TRUE
			WHERE user_id = $1 AND is_read = FALSE;
		`
		res, err := h.DB.Exec(r.Context(), updateQuery, userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "Database error updating all notifications status.",
			})
			return
		}
		rowsAffected = res.RowsAffected()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":       "Notifications marked as read successfully.",
		"updated_count": rowsAffected,
	})
}
