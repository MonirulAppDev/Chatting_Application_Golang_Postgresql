package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/websocket"

	gorillaWS "github.com/gorilla/websocket"
	"mime/multipart"
	"strings"
)

type AuthResponse struct {
	Token   string `json:"token"`
	Message string `json:"message"`
	User    struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
		Email    string `json:"email"`
	} `json:"user"`
	Error string `json:"error"`
}

func TestEndToEndSmokeStep1_RegisterAndLogin(t *testing.T) {
	// 1. Load application config
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// 2. Connect to database and run migrations
	pool, err := database.ConnectDB(cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// 3. Setup test server using exact buildMux
	wsHub := websocket.NewHub(pool)
	mux := buildMux(pool, cfg, wsHub)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	client.Timeout = 5 * time.Second

	now := time.Now().UnixNano()
	userAUsername := fmt.Sprintf("smoke_user_a_%d", now)
	userAEmail := fmt.Sprintf("smoke_user_a_%d@test.com", now)
	userBUsername := fmt.Sprintf("smoke_user_b_%d", now)
	userBEmail := fmt.Sprintf("smoke_user_b_%d@test.com", now)
	password := "SecretPass123!"

	// --- Step 1A: Register User A ---
	regAPayload, _ := json.Marshal(map[string]string{
		"username": userAUsername,
		"email":    userAEmail,
		"password": password,
	})
	respA, err := client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regAPayload))
	if err != nil {
		t.Fatalf("User A register request failed: %v", err)
	}
	defer respA.Body.Close()

	if respA.StatusCode != http.StatusCreated {
		var errResp map[string]any
		_ = json.NewDecoder(respA.Body).Decode(&errResp)
		t.Fatalf("Expected 201 Created for User A register, got %d: %v", respA.StatusCode, errResp)
	}
	t.Logf("User A registered successfully: %s (Status: %d)", userAUsername, respA.StatusCode)

	// --- Step 1B: Register User B ---
	regBPayload, _ := json.Marshal(map[string]string{
		"username": userBUsername,
		"email":    userBEmail,
		"password": password,
	})
	respB, err := client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBPayload))
	if err != nil {
		t.Fatalf("User B register request failed: %v", err)
	}
	defer respB.Body.Close()

	if respB.StatusCode != http.StatusCreated {
		var errResp map[string]any
		_ = json.NewDecoder(respB.Body).Decode(&errResp)
		t.Fatalf("Expected 201 Created for User B register, got %d: %v", respB.StatusCode, errResp)
	}
	t.Logf("User B registered successfully: %s (Status: %d)", userBUsername, respB.StatusCode)

	// --- Step 1C: Login User A ---
	loginAPayload, _ := json.Marshal(map[string]string{
		"email":    userAEmail,
		"password": password,
	})
	respLoginA, err := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginAPayload))
	if err != nil {
		t.Fatalf("User A login request failed: %v", err)
	}
	defer respLoginA.Body.Close()

	if respLoginA.StatusCode != http.StatusOK {
		var errResp map[string]any
		_ = json.NewDecoder(respLoginA.Body).Decode(&errResp)
		t.Fatalf("Expected 200 OK for User A login, got %d: %v", respLoginA.StatusCode, errResp)
	}

	var authA AuthResponse
	if err := json.NewDecoder(respLoginA.Body).Decode(&authA); err != nil {
		t.Fatalf("Failed to decode User A login response: %v", err)
	}
	if authA.Token == "" {
		t.Fatalf("User A token is empty!")
	}
	if authA.User.ID <= 0 {
		t.Fatalf("User A ID is invalid: %d", authA.User.ID)
	}
	t.Logf("User A logged in successfully! UserID: %d, JWT Token (length: %d)", authA.User.ID, len(authA.Token))

	// --- Step 1D: Login User B ---
	loginBPayload, _ := json.Marshal(map[string]string{
		"email":    userBEmail,
		"password": password,
	})
	respLoginB, err := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBPayload))
	if err != nil {
		t.Fatalf("User B login request failed: %v", err)
	}
	defer respLoginB.Body.Close()

	if respLoginB.StatusCode != http.StatusOK {
		var errResp map[string]any
		_ = json.NewDecoder(respLoginB.Body).Decode(&errResp)
		t.Fatalf("Expected 200 OK for User B login, got %d: %v", respLoginB.StatusCode, errResp)
	}

	var authB AuthResponse
	if err := json.NewDecoder(respLoginB.Body).Decode(&authB); err != nil {
		t.Fatalf("Failed to decode User B login response: %v", err)
	}
	if authB.Token == "" {
		t.Fatalf("User B token is empty!")
	}
	if authB.User.ID <= 0 {
		t.Fatalf("User B ID is invalid: %d", authB.User.ID)
	}
	t.Logf("User B logged in successfully! UserID: %d, JWT Token (length: %d)", authB.User.ID, len(authB.Token))

	// --- Validation: Ensure distinct users & tokens ---
	if authA.Token == authB.Token {
		t.Fatalf("User A and User B received the identical token!")
	}
	if authA.User.ID == authB.User.ID {
		t.Fatalf("User A and User B have the same UserID: %d", authA.User.ID)
	}

	t.Logf("Step 1 PASSED: User A (ID: %d) and User B (ID: %d) registered and obtained distinct JWT tokens without any panic or 500 errors.", authA.User.ID, authB.User.ID)
}


func TestEndToEndSmokeStep2_FriendRequestAndAccept(t *testing.T) {
	// 1. Config & DB setup
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	pool, err := database.ConnectDB(cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	wsHub := websocket.NewHub(pool)
	mux := buildMux(pool, cfg, wsHub)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	client.Timeout = 5 * time.Second

	now := time.Now().UnixNano()
	userAEmail := fmt.Sprintf("smoke_fr_a_%d@test.com", now)
	userBEmail := fmt.Sprintf("smoke_fr_b_%d@test.com", now)
	password := "SecretPass123!"

	// Register & Login User A
	regAPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("fr_user_a_%d", now), "email": userAEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regAPayload))

	loginAPayload, _ := json.Marshal(map[string]string{"email": userAEmail, "password": password})
	respLoginA, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginAPayload))
	var authA AuthResponse
	_ = json.NewDecoder(respLoginA.Body).Decode(&authA)
	respLoginA.Body.Close()

	// Register & Login User B
	regBPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("fr_user_b_%d", now), "email": userBEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBPayload))

	loginBPayload, _ := json.Marshal(map[string]string{"email": userBEmail, "password": password})
	respLoginB, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBPayload))
	var authB AuthResponse
	_ = json.NewDecoder(respLoginB.Body).Decode(&authB)
	respLoginB.Body.Close()

	// --- Step 2A: User A sends friend request to User B ---
	reqBody, _ := json.Marshal(map[string]int{"receiver_id": authB.User.ID})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/friends/request", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+authA.Token)
	req.Header.Set("Content-Type", "application/json")

	respReq, err := client.Do(req)
	if err != nil {
		t.Fatalf("Friend request failed: %v", err)
	}
	defer respReq.Body.Close()

	if respReq.StatusCode != http.StatusCreated {
		var errResp map[string]any
		_ = json.NewDecoder(respReq.Body).Decode(&errResp)
		t.Fatalf("Expected 201 Created for friend request, got %d: %v", respReq.StatusCode, errResp)
	}

	var reqResult struct {
		Message       string `json:"message"`
		FriendRequest struct {
			ID         int    `json:"id"`
			SenderID   int    `json:"sender_id"`
			ReceiverID int    `json:"receiver_id"`
			Status     string `json:"status"`
		} `json:"friend_request"`
	}
	if err := json.NewDecoder(respReq.Body).Decode(&reqResult); err != nil {
		t.Fatalf("Failed to decode friend request result: %v", err)
	}

	requestID := reqResult.FriendRequest.ID
	t.Logf("Friend request sent! RequestID: %d, Sender: %d, Receiver: %d, Status: %s", requestID, authA.User.ID, authB.User.ID, reqResult.FriendRequest.Status)

	// --- Step 2B: User B accepts friend request ---
	respondBody, _ := json.Marshal(map[string]any{
		"request_id": requestID,
		"action":     "accepted",
	})
	respAcceptReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/friends/respond", bytes.NewReader(respondBody))
	respAcceptReq.Header.Set("Authorization", "Bearer "+authB.Token)
	respAcceptReq.Header.Set("Content-Type", "application/json")

	respAccept, err := client.Do(respAcceptReq)
	if err != nil {
		t.Fatalf("Friend request respond call failed: %v", err)
	}
	defer respAccept.Body.Close()

	if respAccept.StatusCode != http.StatusOK {
		var errResp map[string]any
		_ = json.NewDecoder(respAccept.Body).Decode(&errResp)
		t.Fatalf("Expected 200 OK for friend accept, got %d: %v", respAccept.StatusCode, errResp)
	}
	t.Logf("User B successfully accepted friend request %d", requestID)

	// --- Step 2C: Verify GET /api/friends for User A ---
	friendsReqA, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/friends", nil)
	friendsReqA.Header.Set("Authorization", "Bearer "+authA.Token)
	respFriendsA, err := client.Do(friendsReqA)
	if err != nil {
		t.Fatalf("Failed to get User A friends: %v", err)
	}
	defer respFriendsA.Body.Close()

	if respFriendsA.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK for GET /api/friends (User A), got %d", respFriendsA.StatusCode)
	}

	var friendsListA struct {
		Friends []struct {
			ID       int    `json:"id"`
			Username string `json:"username"`
		} `json:"friends"`
		Count int `json:"count"`
	}
	_ = json.NewDecoder(respFriendsA.Body).Decode(&friendsListA)

	foundUserB := false
	for _, f := range friendsListA.Friends {
		if f.ID == authB.User.ID {
			foundUserB = true
			break
		}
	}
	if !foundUserB {
		t.Fatalf("User B (ID: %d) not found in User A's friends list: %+v", authB.User.ID, friendsListA.Friends)
	}
	t.Logf("Verified User A's friends list: contains User B (ID: %d), total count: %d", authB.User.ID, friendsListA.Count)

	// --- Step 2D: Verify GET /api/friends for User B ---
	friendsReqB, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/friends", nil)
	friendsReqB.Header.Set("Authorization", "Bearer "+authB.Token)
	respFriendsB, err := client.Do(friendsReqB)
	if err != nil {
		t.Fatalf("Failed to get User B friends: %v", err)
	}
	defer respFriendsB.Body.Close()

	if respFriendsB.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 OK for GET /api/friends (User B), got %d", respFriendsB.StatusCode)
	}

	var friendsListB struct {
		Friends []struct {
			ID       int    `json:"id"`
			Username string `json:"username"`
		} `json:"friends"`
		Count int `json:"count"`
	}
	_ = json.NewDecoder(respFriendsB.Body).Decode(&friendsListB)

	foundUserA := false
	for _, f := range friendsListB.Friends {
		if f.ID == authA.User.ID {
			foundUserA = true
			break
		}
	}
	if !foundUserA {
		t.Fatalf("User A (ID: %d) not found in User B's friends list: %+v", authA.User.ID, friendsListB.Friends)
	}
	t.Logf("Verified User B's friends list: contains User A (ID: %d), total count: %d", authA.User.ID, friendsListB.Count)

	t.Logf("Step 2 PASSED: Friend request sent, accepted, and verified on GET /api/friends for both users with zero errors.")
}


func TestEndToEndSmokeStep3_WebSocketPresence(t *testing.T) {
	// 1. Config & DB setup
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	pool, err := database.ConnectDB(cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	wsHub := websocket.NewHub(pool)
	mux := buildMux(pool, cfg, wsHub)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	client.Timeout = 5 * time.Second

	now := time.Now().UnixNano()
	userAEmail := fmt.Sprintf("smoke_ws_a_%d@test.com", now)
	userBEmail := fmt.Sprintf("smoke_ws_b_%d@test.com", now)
	password := "SecretPass123!"

	// Register & Login User A
	regAPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("ws_user_a_%d", now), "email": userAEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regAPayload))

	loginAPayload, _ := json.Marshal(map[string]string{"email": userAEmail, "password": password})
	respLoginA, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginAPayload))
	var authA AuthResponse
	_ = json.NewDecoder(respLoginA.Body).Decode(&authA)
	respLoginA.Body.Close()

	// Register & Login User B
	regBPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("ws_user_b_%d", now), "email": userBEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBPayload))

	loginBPayload, _ := json.Marshal(map[string]string{"email": userBEmail, "password": password})
	respLoginB, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBPayload))
	var authB AuthResponse
	_ = json.NewDecoder(respLoginB.Body).Decode(&authB)
	respLoginB.Body.Close()

	// User A and User B become friends
	reqBody, _ := json.Marshal(map[string]int{"receiver_id": authB.User.ID})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/friends/request", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+authA.Token)
	req.Header.Set("Content-Type", "application/json")
	respReq, _ := client.Do(req)
	var reqResult struct {
		FriendRequest struct {
			ID int `json:"id"`
		} `json:"friend_request"`
	}
	_ = json.NewDecoder(respReq.Body).Decode(&reqResult)
	respReq.Body.Close()

	respondBody, _ := json.Marshal(map[string]any{"request_id": reqResult.FriendRequest.ID, "action": "accepted"})
	respAccept, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/friends/respond", bytes.NewReader(respondBody))
	respAccept.Header.Set("Authorization", "Bearer "+authB.Token)
	respAccept.Header.Set("Content-Type", "application/json")
	respAcc, _ := client.Do(respAccept)
	respAcc.Body.Close()

	// --- Step 3A: User A connects to WebSocket ---
	wsURLA := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authA.Token
	connA, respWSA, err := gorillaWS.DefaultDialer.Dial(wsURLA, nil)
	if err != nil {
		t.Fatalf("User A WebSocket connection failed: %v", err)
	}
	defer connA.Close()
	if respWSA.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected 101 Switching Protocols for User A, got %d", respWSA.StatusCode)
	}
	t.Logf("User A (ID: %d) connected to WebSocket successfully!", authA.User.ID)

	// User A reads initial online_friends event
	_ = connA.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msgA1, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("User A failed to receive initial event: %v", err)
	}

	var eventA1 struct {
		Type    string `json:"type"`
		UserIDs []int  `json:"user_ids"`
	}
	_ = json.Unmarshal(msgA1, &eventA1)
	if eventA1.Type != "online_friends" {
		t.Fatalf("Expected event type 'online_friends', got %s", eventA1.Type)
	}
	t.Logf("User A received online_friends event: %+v (User B not connected yet)", eventA1.UserIDs)

	// --- Step 3B: User B connects to WebSocket ---
	wsURLB := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authB.Token
	connB, respWSB, err := gorillaWS.DefaultDialer.Dial(wsURLB, nil)
	if err != nil {
		t.Fatalf("User B WebSocket connection failed: %v", err)
	}
	defer connB.Close()
	if respWSB.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("Expected 101 Switching Protocols for User B, got %d", respWSB.StatusCode)
	}
	t.Logf("User B (ID: %d) connected to WebSocket successfully!", authB.User.ID)

	// User B reads initial online_friends event (User A should be in the list!)
	_ = connB.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msgB1, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("User B failed to receive initial event: %v", err)
	}

	var eventB1 struct {
		Type    string `json:"type"`
		UserIDs []int  `json:"user_ids"`
	}
	_ = json.Unmarshal(msgB1, &eventB1)
	if eventB1.Type != "online_friends" {
		t.Fatalf("Expected event type 'online_friends' for User B, got %s", eventB1.Type)
	}

	hasUserA := false
	for _, id := range eventB1.UserIDs {
		if id == authA.User.ID {
			hasUserA = true
			break
		}
	}
	if !hasUserA {
		t.Fatalf("Expected User A (ID: %d) in User B's online_friends list, got: %v", authA.User.ID, eventB1.UserIDs)
	}
	t.Logf("User B received online_friends containing User A (ID: %d)!", authA.User.ID)

	// --- Step 3C: User A should receive user_online event for User B ---
	_ = connA.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msgA2, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("User A failed to receive user_online notification: %v", err)
	}

	var eventA2 struct {
		Type   string `json:"type"`
		UserID int    `json:"user_id"`
	}
	_ = json.Unmarshal(msgA2, &eventA2)
	if eventA2.Type != "user_online" {
		t.Fatalf("Expected event type 'user_online' for User A, got %s", eventA2.Type)
	}
	if eventA2.UserID != authB.User.ID {
		t.Fatalf("Expected user_online UserID %d, got %d", authB.User.ID, eventA2.UserID)
	}
	t.Logf("User A received real-time user_online event for User B (ID: %d)!", authB.User.ID)

	t.Logf("Step 3 PASSED: Both users connected to /ws?token=..., received online_friends list and real-time user_online event with zero errors.")
}


func TestEndToEndSmokeStep4_MessagesMediaAndStickers(t *testing.T) {
	// 1. Config & DB setup
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	pool, err := database.ConnectDB(cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	wsHub := websocket.NewHub(pool)
	mux := buildMux(pool, cfg, wsHub)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	client.Timeout = 5 * time.Second

	now := time.Now().UnixNano()
	userAEmail := fmt.Sprintf("smoke_msg_a_%d@test.com", now)
	userBEmail := fmt.Sprintf("smoke_msg_b_%d@test.com", now)
	password := "SecretPass123!"

	// Register & Login User A
	regAPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("msg_user_a_%d", now), "email": userAEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regAPayload))
	loginAPayload, _ := json.Marshal(map[string]string{"email": userAEmail, "password": password})
	respLoginA, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginAPayload))
	var authA AuthResponse
	_ = json.NewDecoder(respLoginA.Body).Decode(&authA)
	respLoginA.Body.Close()

	// Register & Login User B
	regBPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("msg_user_b_%d", now), "email": userBEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBPayload))
	loginBPayload, _ := json.Marshal(map[string]string{"email": userBEmail, "password": password})
	respLoginB, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBPayload))
	var authB AuthResponse
	_ = json.NewDecoder(respLoginB.Body).Decode(&authB)
	respLoginB.Body.Close()

	// Connect User A and User B via WebSocket
	wsURLA := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authA.Token
	connA, _, err := gorillaWS.DefaultDialer.Dial(wsURLA, nil)
	if err != nil {
		t.Fatalf("User A WebSocket connection failed: %v", err)
	}
	defer connA.Close()

	wsURLB := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authB.Token
	connB, _, err := gorillaWS.DefaultDialer.Dial(wsURLB, nil)
	if err != nil {
		t.Fatalf("User B WebSocket connection failed: %v", err)
	}
	defer connB.Close()

	// 1. User A sends text message
	textMsg := map[string]any{
		"type":         "send_message",
		"receiver_id":  authB.User.ID,
		"message_type": "text",
		"content":      "Hello User B, test message!",
	}
	_ = connA.WriteJSON(textMsg)
	time.Sleep(50 * time.Millisecond)

	// 2. Upload image via POST /api/upload
	imgBody := &bytes.Buffer{}
	imgWriter := multipart.NewWriter(imgBody)
	partImg, _ := imgWriter.CreateFormFile("file", "test_photo.png")
	partImg.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82"))
	imgWriter.Close()

	reqImg, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/upload", imgBody)
	reqImg.Header.Set("Content-Type", imgWriter.FormDataContentType())
	reqImg.Header.Set("Authorization", "Bearer "+authA.Token)
	respImg, err := client.Do(reqImg)
	if err != nil || respImg.StatusCode != http.StatusCreated {
		t.Fatalf("Failed to upload image: %v, status: %d", err, respImg.StatusCode)
	}
	var imgResult struct {
		URL  string `json:"url"`
		Type string `json:"type"`
	}
	_ = json.NewDecoder(respImg.Body).Decode(&imgResult)
	respImg.Body.Close()
	t.Logf("Image uploaded successfully: URL: %s, Type: %s", imgResult.URL, imgResult.Type)

	// User A sends image message
	imgMsg := map[string]any{
		"type":         "send_message",
		"receiver_id":  authB.User.ID,
		"message_type": "image",
		"content":      imgResult.URL,
	}
	_ = connA.WriteJSON(imgMsg)
	time.Sleep(50 * time.Millisecond)

	// 3. Upload audio via POST /api/upload
	audioBody := &bytes.Buffer{}
	audioWriter := multipart.NewWriter(audioBody)
	partAudio, _ := audioWriter.CreateFormFile("file", "voice_note.mp3")
	partAudio.Write([]byte("ID3\x03\x00\x00\x00\x00\x00#TSSE\x00\x00\x00\x0f\x00\x00\x00Lavf58.29.100\x00\xff\xfb\x90d\x00\x00\x00\x00\x00"))
	audioWriter.Close()

	reqAudio, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/upload", audioBody)
	reqAudio.Header.Set("Content-Type", audioWriter.FormDataContentType())
	reqAudio.Header.Set("Authorization", "Bearer "+authA.Token)
	respAudio, err := client.Do(reqAudio)
	if err != nil || respAudio.StatusCode != http.StatusCreated {
		t.Fatalf("Failed to upload audio: %v, status: %d", err, respAudio.StatusCode)
	}
	var audioResult struct {
		URL  string `json:"url"`
		Type string `json:"type"`
	}
	_ = json.NewDecoder(respAudio.Body).Decode(&audioResult)
	respAudio.Body.Close()
	t.Logf("Audio uploaded successfully: URL: %s, Type: %s", audioResult.URL, audioResult.Type)

	// User A sends audio message
	audioMsg := map[string]any{
		"type":         "send_message",
		"receiver_id":  authB.User.ID,
		"message_type": "audio",
		"content":      audioResult.URL,
	}
	_ = connA.WriteJSON(audioMsg)
	time.Sleep(50 * time.Millisecond)

	// 4. Fetch sticker via GET /api/stickers
	respStickers, err := client.Get(ts.URL + "/api/stickers")
	if err != nil || respStickers.StatusCode != http.StatusOK {
		t.Fatalf("Failed to get stickers: %v, status: %d", err, respStickers.StatusCode)
	}
	var stickersResp struct {
		Stickers []struct {
			URL  string `json:"url"`
			Name string `json:"name"`
		} `json:"stickers"`
	}
	_ = json.NewDecoder(respStickers.Body).Decode(&stickersResp)
	respStickers.Body.Close()

	stickerURL := "/static/stickers/pack1/sticker1.png"
	if len(stickersResp.Stickers) > 0 {
		stickerURL = stickersResp.Stickers[0].URL
	}
	t.Logf("Selected sticker URL: %s", stickerURL)

	// User A sends sticker message
	stickerMsg := map[string]any{
		"type":         "send_message",
		"receiver_id":  authB.User.ID,
		"message_type": "sticker",
		"content":      stickerURL,
	}
	_ = connA.WriteJSON(stickerMsg)
	time.Sleep(50 * time.Millisecond)

	// 5. User A sends URL link message
	urlMsg := map[string]any{
		"type":         "send_message",
		"receiver_id":  authB.User.ID,
		"message_type": "text",
		"content":      "Check out this site: https://golang.org/doc",
	}
	_ = connA.WriteJSON(urlMsg)
	time.Sleep(200 * time.Millisecond)

	// 6. Verify persistence with GET /api/messages?with=<authB.User.ID> (as User A)
	msgReqA, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/messages?with=%d", ts.URL, authB.User.ID), nil)
	msgReqA.Header.Set("Authorization", "Bearer "+authA.Token)
	respMsgsA, err := client.Do(msgReqA)
	if err != nil || respMsgsA.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch messages for User A: %v, status: %d", err, respMsgsA.StatusCode)
	}
	defer respMsgsA.Body.Close()

	var chatHistoryA struct {
		Messages []struct {
			ID          int    `json:"id"`
			SenderID    int    `json:"sender_id"`
			ReceiverID  int    `json:"receiver_id"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"messages"`
		Count int `json:"count"`
	}
	_ = json.NewDecoder(respMsgsA.Body).Decode(&chatHistoryA)

	if len(chatHistoryA.Messages) < 5 {
		t.Fatalf("Expected at least 5 persisted messages, found %d: %+v", len(chatHistoryA.Messages), chatHistoryA.Messages)
	}

	foundText := false
	foundImage := false
	foundAudio := false
	foundSticker := false
	foundURL := false

	for _, m := range chatHistoryA.Messages {
		switch m.MessageType {
		case "text":
			if strings.Contains(m.Content, "https://golang.org") {
				foundURL = true
			} else {
				foundText = true
			}
		case "image":
			if strings.Contains(m.Content, "/uploads/images/") {
				foundImage = true
			}
		case "audio":
			if strings.Contains(m.Content, "/uploads/audio/") {
				foundAudio = true
			}
		case "sticker":
			if strings.Contains(m.Content, "/static/stickers/") {
				foundSticker = true
			}
		}
	}

	if !foundText || !foundImage || !foundAudio || !foundSticker || !foundURL {
		t.Fatalf("Missing expected message types: text=%v, image=%v, audio=%v, sticker=%v, url=%v", foundText, foundImage, foundAudio, foundSticker, foundURL)
	}

	t.Logf("User A verified %d persisted messages: text, image, audio, sticker, and URL all stored correctly!", len(chatHistoryA.Messages))

	// 7. Verify User B can also read the exact same conversation with GET /api/messages?with=<authA.User.ID>
	msgReqB, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/messages?with=%d", ts.URL, authA.User.ID), nil)
	msgReqB.Header.Set("Authorization", "Bearer "+authB.Token)
	respMsgsB, err := client.Do(msgReqB)
	if err != nil || respMsgsB.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch messages for User B: %v, status: %d", err, respMsgsB.StatusCode)
	}
	defer respMsgsB.Body.Close()

	var chatHistoryB struct {
		Messages []struct {
			ID int `json:"id"`
		} `json:"messages"`
		Count int `json:"count"`
	}
	_ = json.NewDecoder(respMsgsB.Body).Decode(&chatHistoryB)

	if len(chatHistoryB.Messages) < 5 {
		t.Fatalf("User B expected at least 5 persisted messages, found %d", len(chatHistoryB.Messages))
	}

	t.Logf("User B verified %d persisted messages successfully!", len(chatHistoryB.Messages))
	t.Logf("Step 4 PASSED: Text, Image upload, Audio upload, Sticker, and URL link messages all successfully sent, persisted in PostgreSQL, and fetched via GET /api/messages with zero errors.")
}


func TestEndToEndSmokeStep5_OfflineMessageNotification(t *testing.T) {
	// 1. Config & DB setup
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	pool, err := database.ConnectDB(cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	wsHub := websocket.NewHub(pool)
	mux := buildMux(pool, cfg, wsHub)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	client.Timeout = 5 * time.Second

	now := time.Now().UnixNano()
	userAEmail := fmt.Sprintf("smoke_off_a_%d@test.com", now)
	userBEmail := fmt.Sprintf("smoke_off_b_%d@test.com", now)
	password := "SecretPass123!"

	// Register & Login User A
	regAPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("off_user_a_%d", now), "email": userAEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regAPayload))
	loginAPayload, _ := json.Marshal(map[string]string{"email": userAEmail, "password": password})
	respLoginA, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginAPayload))
	var authA AuthResponse
	_ = json.NewDecoder(respLoginA.Body).Decode(&authA)
	respLoginA.Body.Close()

	// Register & Login User B
	regBPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("off_user_b_%d", now), "email": userBEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBPayload))
	loginBPayload, _ := json.Marshal(map[string]string{"email": userBEmail, "password": password})
	respLoginB, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBPayload))
	var authB AuthResponse
	_ = json.NewDecoder(respLoginB.Body).Decode(&authB)
	respLoginB.Body.Close()

	// Establish friendship between User A and User B
	reqBody, _ := json.Marshal(map[string]int{"receiver_id": authB.User.ID})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/friends/request", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+authA.Token)
	req.Header.Set("Content-Type", "application/json")
	respReq, _ := client.Do(req)
	var reqResult struct {
		FriendRequest struct {
			ID int `json:"id"`
		} `json:"friend_request"`
	}
	_ = json.NewDecoder(respReq.Body).Decode(&reqResult)
	respReq.Body.Close()

	respondBody, _ := json.Marshal(map[string]any{"request_id": reqResult.FriendRequest.ID, "action": "accepted"})
	respAccept, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/friends/respond", bytes.NewReader(respondBody))
	respAccept.Header.Set("Authorization", "Bearer "+authB.Token)
	respAccept.Header.Set("Content-Type", "application/json")
	respAcc, _ := client.Do(respAccept)
	respAcc.Body.Close()

	// --- Step 5A: Connect both users to WebSocket ---
	wsURLA := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authA.Token
	connA, _, err := gorillaWS.DefaultDialer.Dial(wsURLA, nil)
	if err != nil {
		t.Fatalf("User A WebSocket connection failed: %v", err)
	}
	defer connA.Close()

	wsURLB := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authB.Token
	connB, _, err := gorillaWS.DefaultDialer.Dial(wsURLB, nil)
	if err != nil {
		t.Fatalf("User B WebSocket connection failed: %v", err)
	}

	// Drain initial presence events
	_ = connA.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, _, _ = connA.ReadMessage()
	_ = connB.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, _, _ = connB.ReadMessage()

	// --- Step 5B: User B disconnects (goes offline) ---
	_ = connB.Close()
	time.Sleep(100 * time.Millisecond) // Allow hub unregistration to complete
	t.Logf("User B (ID: %d) disconnected and went offline.", authB.User.ID)

	// --- Step 5C: User A sends message to offline User B ---
	offlineContent := "Hey User B, you are offline right now!"
	offlineMsg := map[string]any{
		"type":         "send_message",
		"receiver_id":  authB.User.ID,
		"message_type": "text",
		"content":      offlineContent,
	}
	_ = connA.WriteJSON(offlineMsg)
	time.Sleep(100 * time.Millisecond) // Allow message save and offline notification insertion
	t.Logf("User A sent message to offline User B.")

	// --- Step 5D: User B reconnects to WebSocket ---
	connB2, _, err := gorillaWS.DefaultDialer.Dial(wsURLB, nil)
	if err != nil {
		t.Fatalf("User B WebSocket reconnection failed: %v", err)
	}
	defer connB2.Close()
	t.Logf("User B reconnected to WebSocket successfully.")

	// --- Step 5E: User B calls GET /api/notifications to check unread notification ---
	notifReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/notifications", nil)
	notifReq.Header.Set("Authorization", "Bearer "+authB.Token)
	respNotif, err := client.Do(notifReq)
	if err != nil || respNotif.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch notifications for User B: %v, status: %d", err, respNotif.StatusCode)
	}
	defer respNotif.Body.Close()

	var notifResp struct {
		Notifications []struct {
			ID       int    `json:"id"`
			UserID   int    `json:"user_id"`
			SenderID int    `json:"sender_id"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			Body     string `json:"body"`
			IsRead   bool   `json:"is_read"`
		} `json:"notifications"`
		Count int `json:"count"`
	}
	_ = json.NewDecoder(respNotif.Body).Decode(&notifResp)

	if len(notifResp.Notifications) == 0 {
		t.Fatalf("User B received 0 unread notifications! Expected at least 1 offline notification.")
	}

	foundOfflineNotif := false
	for _, n := range notifResp.Notifications {
		if n.SenderID == authA.User.ID && n.Type == "message" && !n.IsRead {
			if strings.Contains(n.Body, offlineContent) {
				foundOfflineNotif = true
				t.Logf("Found expected unread notification: ID=%d, Sender=%d, Title=%q, Body=%q, IsRead=%v",
					n.ID, n.SenderID, n.Title, n.Body, n.IsRead)
				break
			}
		}
	}

	if !foundOfflineNotif {
		t.Fatalf("Expected offline message notification not found in: %+v", notifResp.Notifications)
	}

	t.Logf("Step 5 PASSED: User B disconnected, User A sent message, notification was saved in notifications table, and User B retrieved it via GET /api/notifications with zero errors.")
}


func TestEndToEndSmokeStep6_LiveLocationStreamingAndPersist(t *testing.T) {
	// 1. Config & DB setup
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	pool, err := database.ConnectDB(cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	wsHub := websocket.NewHub(pool)
	mux := buildMux(pool, cfg, wsHub)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	client.Timeout = 5 * time.Second

	now := time.Now().UnixNano()
	userAEmail := fmt.Sprintf("smoke_loc_a_%d@test.com", now)
	userBEmail := fmt.Sprintf("smoke_loc_b_%d@test.com", now)
	password := "SecretPass123!"

	// Register & Login User A
	regAPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("loc_user_a_%d", now), "email": userAEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regAPayload))
	loginAPayload, _ := json.Marshal(map[string]string{"email": userAEmail, "password": password})
	respLoginA, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginAPayload))
	var authA AuthResponse
	_ = json.NewDecoder(respLoginA.Body).Decode(&authA)
	respLoginA.Body.Close()

	// Register & Login User B
	regBPayload, _ := json.Marshal(map[string]string{"username": fmt.Sprintf("loc_user_b_%d", now), "email": userBEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBPayload))
	loginBPayload, _ := json.Marshal(map[string]string{"email": userBEmail, "password": password})
	respLoginB, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBPayload))
	var authB AuthResponse
	_ = json.NewDecoder(respLoginB.Body).Decode(&authB)
	respLoginB.Body.Close()

	// Establish friendship
	reqBody, _ := json.Marshal(map[string]int{"receiver_id": authB.User.ID})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/friends/request", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+authA.Token)
	req.Header.Set("Content-Type", "application/json")
	respReq, _ := client.Do(req)
	var reqResult struct {
		FriendRequest struct {
			ID int `json:"id"`
		} `json:"friend_request"`
	}
	_ = json.NewDecoder(respReq.Body).Decode(&reqResult)
	respReq.Body.Close()

	respondBody, _ := json.Marshal(map[string]any{"request_id": reqResult.FriendRequest.ID, "action": "accepted"})
	respAccept, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/friends/respond", bytes.NewReader(respondBody))
	respAccept.Header.Set("Authorization", "Bearer "+authB.Token)
	respAccept.Header.Set("Content-Type", "application/json")
	respAcc, _ := client.Do(respAccept)
	respAcc.Body.Close()

	// Connect both users to WebSocket
	wsURLA := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authA.Token
	connA, _, err := gorillaWS.DefaultDialer.Dial(wsURLA, nil)
	if err != nil {
		t.Fatalf("User A WebSocket connection failed: %v", err)
	}
	defer connA.Close()

	wsURLB := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authB.Token
	connB, _, err := gorillaWS.DefaultDialer.Dial(wsURLB, nil)
	if err != nil {
		t.Fatalf("User B WebSocket connection failed: %v", err)
	}
	defer connB.Close()

	// Drain initial presence events
	_ = connA.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, _ = connA.ReadMessage()
	_ = connB.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, _ = connB.ReadMessage()

	// --- Step 6A: User A sends live_location_start ---
	startLoc := map[string]any{
		"type":        "live_location_start",
		"receiver_id": authB.User.ID,
		"latitude":    23.8103,
		"longitude":   90.4125,
		"duration":    60,
	}
	_ = connA.WriteJSON(startLoc)

	// User B reads live_location_start relay
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgStartB, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("User B failed to receive live_location_start relay: %v", err)
	}
	var startRelay struct {
		Type      string  `json:"type"`
		SenderID  int     `json:"sender_id"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	_ = json.Unmarshal(msgStartB, &startRelay)
	if startRelay.Type != "live_location_start" || startRelay.SenderID != authA.User.ID {
		t.Fatalf("Unexpected live_location_start relay: %+v", startRelay)
	}
	t.Logf("User B received live_location_start relay: from %d at (%f, %f)", startRelay.SenderID, startRelay.Latitude, startRelay.Longitude)

	// --- Step 6B: User A sends live_location_update ---
	updateLoc := map[string]any{
		"type": "live_location_update",
		"to":   authB.User.ID,
		"lat":  23.8125,
		"lng":  90.4145,
	}
	_ = connA.WriteJSON(updateLoc)

	// User B reads live_location relay tick
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgUpdateB, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("User B failed to receive live_location relay: %v", err)
	}
	var updateRelay struct {
		Type string  `json:"type"`
		From int     `json:"from"`
		Lat  float64 `json:"lat"`
		Lng  float64 `json:"lng"`
	}
	_ = json.Unmarshal(msgUpdateB, &updateRelay)
	if updateRelay.Type != "live_location" || updateRelay.From != authA.User.ID {
		t.Fatalf("Unexpected live_location update relay: %+v", updateRelay)
	}
	t.Logf("User B received real-time live_location tick: from %d at (%f, %f)", updateRelay.From, updateRelay.Lat, updateRelay.Lng)

	// --- Step 6C: User A sends live_location_stop ---
	stopLoc := map[string]any{
		"type":        "live_location_stop",
		"receiver_id": authB.User.ID,
	}
	_ = connA.WriteJSON(stopLoc)

	// User B reads live_location_stop relay
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgStopB, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("User B failed to receive live_location_stop relay: %v", err)
	}
	var stopRelay struct {
		Type     string `json:"type"`
		SenderID int    `json:"sender_id"`
	}
	_ = json.Unmarshal(msgStopB, &stopRelay)
	if stopRelay.Type != "live_location_stop" || stopRelay.SenderID != authA.User.ID {
		t.Fatalf("Unexpected live_location_stop relay: %+v", stopRelay)
	}
	t.Logf("User B received live_location_stop relay from %d", stopRelay.SenderID)

	time.Sleep(100 * time.Millisecond) // Allow DB persistence of final location message

	// --- Step 6D: Verify Database Persistence via GET /api/messages ---
	msgReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/messages?with=%d", ts.URL, authB.User.ID), nil)
	msgReq.Header.Set("Authorization", "Bearer "+authA.Token)
	respMsgs, err := client.Do(msgReq)
	if err != nil || respMsgs.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch messages: %v, status: %d", err, respMsgs.StatusCode)
	}
	defer respMsgs.Body.Close()

	var chatHistory struct {
		Messages []struct {
			ID          int             `json:"id"`
			MessageType string          `json:"message_type"`
			Content     string          `json:"content"`
			Metadata    json.RawMessage `json:"metadata"`
		} `json:"messages"`
		Count int `json:"count"`
	}
	_ = json.NewDecoder(respMsgs.Body).Decode(&chatHistory)

	foundLocationMsg := false
	for _, m := range chatHistory.Messages {
		if m.MessageType == "location" {
			foundLocationMsg = true
			var meta struct {
				Lat       float64 `json:"lat"`
				Lng       float64 `json:"lng"`
				LiveEnded bool    `json:"live_ended"`
			}
			_ = json.Unmarshal(m.Metadata, &meta)
			if meta.Lat != 23.8125 || meta.Lng != 90.4145 || !meta.LiveEnded {
				t.Fatalf("Unexpected metadata in final location message: %+v", meta)
			}
			t.Logf("Final location message verified in DB: ID=%d, Content=%q, Metadata=%s",
				m.ID, m.Content, string(m.Metadata))
			break
		}
	}

	if !foundLocationMsg {
		t.Fatalf("Location message not found in persisted chat history: %+v", chatHistory.Messages)
	}

	t.Logf("Step 6 PASSED: live_location_start -> live_location_update -> live_location_stop streamed in real-time and final location coordinate persisted in DB with zero errors.")
}


func TestEndToEndSmokeStep7_CallSignalingAndHistory(t *testing.T) {
	// 1. Config & DB setup
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	pool, err := database.ConnectDB(cfg)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	wsHub := websocket.NewHub(pool)
	mux := buildMux(pool, cfg, wsHub)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()
	client.Timeout = 5 * time.Second

	now := time.Now().UnixNano()
	userAName := fmt.Sprintf("call_user_a_%d", now)
	userAEmail := fmt.Sprintf("smoke_call_a_%d@test.com", now)
	userBName := fmt.Sprintf("call_user_b_%d", now)
	userBEmail := fmt.Sprintf("smoke_call_b_%d@test.com", now)
	password := "SecretPass123!"

	// Register & Login User A
	regAPayload, _ := json.Marshal(map[string]string{"username": userAName, "email": userAEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regAPayload))
	loginAPayload, _ := json.Marshal(map[string]string{"email": userAEmail, "password": password})
	respLoginA, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginAPayload))
	var authA AuthResponse
	_ = json.NewDecoder(respLoginA.Body).Decode(&authA)
	respLoginA.Body.Close()

	// Register & Login User B
	regBPayload, _ := json.Marshal(map[string]string{"username": userBName, "email": userBEmail, "password": password})
	_, _ = client.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(regBPayload))
	loginBPayload, _ := json.Marshal(map[string]string{"email": userBEmail, "password": password})
	respLoginB, _ := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(loginBPayload))
	var authB AuthResponse
	_ = json.NewDecoder(respLoginB.Body).Decode(&authB)
	respLoginB.Body.Close()

	// Connect both users to WebSocket
	wsURLA := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authA.Token
	connA, _, err := gorillaWS.DefaultDialer.Dial(wsURLA, nil)
	if err != nil {
		t.Fatalf("User A WebSocket connection failed: %v", err)
	}
	defer connA.Close()

	wsURLB := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + authB.Token
	connB, _, err := gorillaWS.DefaultDialer.Dial(wsURLB, nil)
	if err != nil {
		t.Fatalf("User B WebSocket connection failed: %v", err)
	}
	defer connB.Close()

	// Drain initial presence events
	_ = connA.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, _ = connA.ReadMessage()
	_ = connB.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, _ = connB.ReadMessage()

	callID := fmt.Sprintf("test_call_%d", now)

	// --- Step 7A: User A sends call_offer ---
	offerPayload := map[string]any{
		"type":        "call_offer",
		"call_id":     callID,
		"receiver_id": authB.User.ID,
		"call_type":   "video",
		"sdp":         map[string]string{"type": "offer", "sdp": "v=0\r\no=caller 123..."},
	}
	_ = connA.WriteJSON(offerPayload)

	// User B reads call_offer relay
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgOfferB, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("User B failed to receive call_offer relay: %v", err)
	}
	var offerRelay struct {
		Type     string          `json:"type"`
		CallID   string          `json:"call_id"`
		CallerID int             `json:"caller_id"`
		CallType string          `json:"call_type"`
		SDP      json.RawMessage `json:"sdp"`
	}
	_ = json.Unmarshal(msgOfferB, &offerRelay)
	if offerRelay.Type != "call_offer" || offerRelay.CallerID != authA.User.ID || offerRelay.CallType != "video" {
		t.Fatalf("Unexpected call_offer relay received by User B: %+v", offerRelay)
	}
	t.Logf("User B received call_offer relay: CallID=%s, CallerID=%d, CallType=%s",
		offerRelay.CallID, offerRelay.CallerID, offerRelay.CallType)

	// --- Step 7B: User B sends call_answer ---
	answerPayload := map[string]any{
		"type":      "call_answer",
		"call_id":   callID,
		"caller_id": authA.User.ID,
		"sdp":       map[string]string{"type": "answer", "sdp": "v=0\r\no=receiver 456..."},
	}
	_ = connB.WriteJSON(answerPayload)

	// User A reads call_answer relay
	_ = connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgAnswerA, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("User A failed to receive call_answer relay: %v", err)
	}
	var answerRelay struct {
		Type       string          `json:"type"`
		CallID     string          `json:"call_id"`
		SenderID   int             `json:"sender_id"`
		ReceiverID int             `json:"receiver_id"`
		SDP        json.RawMessage `json:"sdp"`
	}
	_ = json.Unmarshal(msgAnswerA, &answerRelay)
	if answerRelay.Type != "call_answer" || answerRelay.SenderID != authB.User.ID {
		t.Fatalf("Unexpected call_answer relay received by User A: %+v", answerRelay)
	}
	t.Logf("User A received call_answer relay: CallID=%s, SenderID=%d", answerRelay.CallID, answerRelay.SenderID)

	// --- Step 7C: User A sends ice_candidate ---
	icePayloadA := map[string]any{
		"type":        "ice_candidate",
		"call_id":     callID,
		"receiver_id": authB.User.ID,
		"candidate":   map[string]any{"candidate": "candidate:1 1 UDP ...", "sdpMid": "0"},
	}
	_ = connA.WriteJSON(icePayloadA)

	// User B reads ice_candidate relay
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgIceB, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("User B failed to receive ice_candidate relay: %v", err)
	}
	var iceRelayB struct {
		Type      string          `json:"type"`
		SenderID  int             `json:"sender_id"`
		Candidate json.RawMessage `json:"candidate"`
	}
	_ = json.Unmarshal(msgIceB, &iceRelayB)
	if iceRelayB.Type != "ice_candidate" || iceRelayB.SenderID != authA.User.ID {
		t.Fatalf("Unexpected ice_candidate relay received by User B: %+v", iceRelayB)
	}
	t.Logf("User B received ice_candidate relay from %d", iceRelayB.SenderID)

	// Simulate call duration
	time.Sleep(1 * time.Second)

	// --- Step 7D: User A sends call_end ---
	endPayload := map[string]any{
		"type":        "call_end",
		"call_id":     callID,
		"receiver_id": authB.User.ID,
	}
	_ = connA.WriteJSON(endPayload)

	// User B reads call_end relay
	_ = connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msgEndB, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("User B failed to receive call_end relay: %v", err)
	}
	var endRelay struct {
		Type            string `json:"type"`
		SenderID        int    `json:"sender_id"`
		DurationSeconds int    `json:"duration_seconds"`
	}
	_ = json.Unmarshal(msgEndB, &endRelay)
	if endRelay.Type != "call_end" || endRelay.SenderID != authA.User.ID {
		t.Fatalf("Unexpected call_end relay received by User B: %+v", endRelay)
	}
	t.Logf("User B received call_end relay: SenderID=%d, DurationSeconds=%d",
		endRelay.SenderID, endRelay.DurationSeconds)

	time.Sleep(100 * time.Millisecond) // Allow DB update

	// --- Step 7E: Verify GET /api/calls for User A ---
	callsReqA, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/calls", nil)
	callsReqA.Header.Set("Authorization", "Bearer "+authA.Token)
	respCallsA, err := client.Do(callsReqA)
	if err != nil || respCallsA.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch calls for User A: %v, status: %d", err, respCallsA.StatusCode)
	}
	defer respCallsA.Body.Close()

	var callHistoryA struct {
		Calls []struct {
			ID               int        `json:"id"`
			CallerID         int        `json:"caller_id"`
			ReceiverID       int        `json:"receiver_id"`
			CallType         string     `json:"call_type"`
			Status           string     `json:"status"`
			DurationSeconds  int        `json:"duration_seconds"`
			StartedAt        *time.Time `json:"started_at"`
			CallerUsername   string     `json:"caller_username"`
			ReceiverUsername string     `json:"receiver_username"`
		} `json:"calls"`
		Count int `json:"count"`
	}
	_ = json.NewDecoder(respCallsA.Body).Decode(&callHistoryA)

	if len(callHistoryA.Calls) == 0 {
		t.Fatalf("User A has 0 call logs! Expected at least 1 completed call log.")
	}

	foundCall := false
	for _, c := range callHistoryA.Calls {
		if c.CallerID == authA.User.ID && c.ReceiverID == authB.User.ID && c.CallType == "video" {
			if c.Status == "completed" {
				foundCall = true
				if c.StartedAt == nil {
					t.Fatalf("Expected non-nil started_at timestamp in completed call log")
				}
				t.Logf("User A verified completed call log: ID=%d, Caller=%s, Receiver=%s, Status=%s, Duration=%ds, StartedAt=%v",
					c.ID, c.CallerUsername, c.ReceiverUsername, c.Status, c.DurationSeconds, *c.StartedAt)
				break
			}
		}
	}

	if !foundCall {
		t.Fatalf("Expected completed video call log not found: %+v", callHistoryA.Calls)
	}

	// --- Step 7F: Verify GET /api/calls for User B ---
	callsReqB, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/calls", nil)
	callsReqB.Header.Set("Authorization", "Bearer "+authB.Token)
	respCallsB, err := client.Do(callsReqB)
	if err != nil || respCallsB.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch calls for User B: %v, status: %d", err, respCallsB.StatusCode)
	}
	defer respCallsB.Body.Close()

	var callHistoryB struct {
		Calls []struct {
			ID     int    `json:"id"`
			Status string `json:"status"`
		} `json:"calls"`
		Count int `json:"count"`
	}
	_ = json.NewDecoder(respCallsB.Body).Decode(&callHistoryB)

	if len(callHistoryB.Calls) == 0 {
		t.Fatalf("User B has 0 call logs! Expected at least 1 call log.")
	}
	t.Logf("User B verified %d call logs successfully!", len(callHistoryB.Calls))

	t.Logf("Step 7 PASSED: call_offer -> call_answer -> ice_candidate -> call_end signaling completed, and call history verified on GET /api/calls with status='completed' and duration.")
}
