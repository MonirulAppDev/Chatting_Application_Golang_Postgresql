// Package websocket manages real-time bi-directional communication, client sessions, and messaging hub.
package websocket

import (
	"context"
	"encoding/json"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
OnlineFriendsEvent is sent to a newly connected user containing the IDs of their
accepted friends who are currently online and connected to the WebSocket.
*/
type OnlineFriendsEvent struct {
	Type    string `json:"type"`
	UserIDs []int  `json:"user_ids"`
}

/*
UserOnlineEvent is dispatched to each online friend notifying them that a specific
user has come online.
*/
type UserOnlineEvent struct {
	Type   string `json:"type"`
	UserID int    `json:"user_id"`
}

/*
UserOfflineEvent is dispatched to each online friend notifying them that a specific
user has disconnected and gone offline.
*/
type UserOfflineEvent struct {
	Type   string `json:"type"`
	UserID int    `json:"user_id"`
}

/*
GetAcceptedFriendIDs queries the friendships table in the database and returns a slice
containing all user IDs that have an 'accepted' friendship with the given userID.
It checks both cases where the user was either the sender or the receiver of the friend request.
*/
func GetAcceptedFriendIDs(ctx context.Context, db *pgxpool.Pool, userID int) ([]int, error) {
	query := `
		SELECT 
			CASE 
				WHEN sender_id = $1 THEN receiver_id 
				ELSE sender_id 
			END AS friend_id
		FROM friendships
		WHERE (sender_id = $1 OR receiver_id = $1)
		  AND status = 'accepted';
	`

	rows, err := db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	friendIDs := make([]int, 0)
	for rows.Next() {
		var friendID int
		if err := rows.Scan(&friendID); err != nil {
			return nil, err
		}
		friendIDs = append(friendIDs, friendID)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return friendIDs, nil
}

// GetAcceptedFriendIDs is a convenience method on Hub to fetch friend IDs using the Hub's database pool.
func (h *Hub) GetAcceptedFriendIDs(ctx context.Context, userID int) ([]int, error) {
	return GetAcceptedFriendIDs(ctx, h.DB, userID)
}

/*
NotifyUserOnline coordinates real-time presence when a user connects:
1. Queries the user's accepted friends from the database.
2. Identifies which of those friends are currently online in Hub.Clients.
3. Dispatches an "online_friends" event to the newly connected user.
4. Dispatches a "user_online" event to each active online friend.
*/
func (h *Hub) NotifyUserOnline(ctx context.Context, userID int) {
	// Step 1: Query accepted friends from database
	friendIDs, err := h.GetAcceptedFriendIDs(ctx, userID)
	if err != nil {
		log.Printf("Error fetching accepted friend IDs for user %d: %v", userID, err)
		return
	}

	// Step 2: Determine which friends are currently online in Hub.Clients
	onlineFriendIDs := make([]int, 0)
	h.Mutex.RLock()
	for _, friendID := range friendIDs {
		if _, online := h.Clients[friendID]; online {
			onlineFriendIDs = append(onlineFriendIDs, friendID)
		}
	}
	h.Mutex.RUnlock()

	// Step 3: Send "online_friends" event to the newly connected user
	onlineFriendsEvent := OnlineFriendsEvent{
		Type:    "online_friends",
		UserIDs: onlineFriendIDs,
	}
	if payload, err := json.Marshal(onlineFriendsEvent); err == nil {
		h.SendToUser(userID, payload)
	}

	// Step 4: Send "user_online" event to each friend currently online
	userOnlineEvent := UserOnlineEvent{
		Type:   "user_online",
		UserID: userID,
	}
	if payload, err := json.Marshal(userOnlineEvent); err == nil {
		for _, friendID := range onlineFriendIDs {
			h.SendToUser(friendID, payload)
		}
	}
}

/*
NotifyUserOffline coordinates real-time presence when a user disconnects:
1. Calls Hub.Unregister(userID) to remove the user from Hub.Clients and close their Send channel.
2. Updates the user's last_seen timestamp in the database.
3. Queries the user's accepted friends from the database.
4. Identifies which of those friends are currently still online.
5. Dispatches a "user_offline" event to each active online friend.
*/
func (h *Hub) NotifyUserOffline(ctx context.Context, userID int) {
	// Step 1: Unregister user from the active clients map
	h.Unregister(userID)

	// Step 2: Update last_seen timestamp in database
	if _, err := h.DB.Exec(ctx, "UPDATE users SET last_seen = NOW() WHERE id = $1;", userID); err != nil {
		log.Printf("Failed to update last_seen for user %d: %v", userID, err)
	}

	// Step 3: Query accepted friends from database
	friendIDs, err := h.GetAcceptedFriendIDs(ctx, userID)
	if err != nil {
		log.Printf("Error fetching accepted friend IDs for offline user %d: %v", userID, err)
		return
	}

	// Step 4: Determine which friends are currently still online in Hub.Clients
	onlineFriendIDs := make([]int, 0)
	h.Mutex.RLock()
	for _, friendID := range friendIDs {
		if _, online := h.Clients[friendID]; online {
			onlineFriendIDs = append(onlineFriendIDs, friendID)
		}
	}
	h.Mutex.RUnlock()

	// Step 5: Send "user_offline" event to each friend currently online
	userOfflineEvent := UserOfflineEvent{
		Type:   "user_offline",
		UserID: userID,
	}
	if payload, err := json.Marshal(userOfflineEvent); err == nil {
		for _, friendID := range onlineFriendIDs {
			h.SendToUser(friendID, payload)
		}
	}
}
