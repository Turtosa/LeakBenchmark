package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

var globalSetup Setup = Setup{
	Id: "0",
	BaseURL: "https://api.openai.com",
}

type Setup struct {
	Id string `json:"id"`
	BaseURL string `json:"baseURL"`
}

var db *sql.DB

func initDB() error {
	var err error
	db, err = sql.Open("sqlite3", "./messages.db")
	if err != nil {
		return err
	}

	createTableSQL := `CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		content TEXT NOT NULL,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	_, err = db.Exec(createTableSQL)
	return err
}

func saveMessage(sessionID, content string) error {
	insertSQL := `INSERT INTO messages (session_id, content) VALUES (?, ?)`
	_, err := db.Exec(insertSQL, sessionID, content)
	return err
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	if err := saveMessage(globalSetup.Id, string(body)); err != nil {
		log.Printf("Failed to save message: %v", err)
	}

	target, err := url.Parse(globalSetup.BaseURL)
	if err != nil {
		http.Error(w, "Failed to parse target URL", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		req.URL.Host = target.Host
		req.URL.Scheme = target.Scheme

		req.URL.RawQuery = ""
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			req.URL.Path = "/v1/chat/completions"
		} else if !strings.HasPrefix(path, "/") {
			req.URL.Path = "/" + path
		} else {
			req.URL.Path = path
		}
		if !strings.HasPrefix(req.URL.Path, "/v1") {
			req.URL.Path = fmt.Sprintf("/v1%s", req.URL.Path)
		}
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Header.Get("Content-Type") == "text/event-stream" {
			return nil
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	proxy.ServeHTTP(w, r)
}

func streamingProxyHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	if err := saveMessage(globalSetup.Id, string(body)); err != nil {
		log.Printf("Failed to save message: %v", err)
	}

	target, err := url.Parse(globalSetup.BaseURL)
	if err != nil {
		http.Error(w, "Failed to parse target URL", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		req.URL.Host = target.Host
		req.URL.Scheme = target.Scheme

		req.URL.RawQuery = ""
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			req.URL.Path = "/v1/chat/completions"
		} else if !strings.HasPrefix(path, "/") {
			req.URL.Path = "/" + path
		} else {
			req.URL.Path = path
		}
		if !strings.HasPrefix(req.URL.Path, "/v1") {
			req.URL.Path = fmt.Sprintf("/v1%s", req.URL.Path)
		}
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Header.Get("Content-Type") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("Access-Control-Allow-Origin", "*")

			for key, values := range resp.Header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}

			w.WriteHeader(resp.StatusCode)

			var streamBuffer bytes.Buffer

			_, err := io.Copy(io.MultiWriter(w, &streamBuffer), resp.Body)
			if err != nil {
				log.Printf("Error streaming response: %v", err)
			}

			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}

			return nil
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	proxy.ServeHTTP(w, r)
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	var setup Setup
	if err := json.Unmarshal(body, &setup); err != nil {
		http.Error(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}
	if setup.BaseURL != "" && setup.Id != "" {
		globalSetup = setup
		return
	}

	var openaiReq struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &openaiReq); err != nil {
		http.Error(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	if openaiReq.Stream {
		streamingProxyHandler(w, r)
	} else {
		proxyHandler(w, r)
	}
}

func main() {
	if err := initDB(); err != nil {
		log.Fatal("Failed to initialize database:", err)
	}
	defer db.Close()

	http.HandleFunc("/", handleRequest)

	port := ":8080"
	fmt.Printf("OpenAI Proxy server starting on port %s\n", port)
	fmt.Printf("Usage: http://localhost%s/v1/chat/completions?id=your_session_id\n", port)

	log.Fatal(http.ListenAndServe(port, nil))
}
