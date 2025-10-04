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

type Message struct {
	Role    string `json:"role"`
	Content any `json:"content"`
}

type OpenAIRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
}

type OpenAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta,omitempty"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
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
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		model TEXT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	_, err = db.Exec(createTableSQL)
	return err
}

func saveMessage(sessionID, role, content, model string) error {
	insertSQL := `INSERT INTO messages (session_id, role, content, model) VALUES (?, ?, ?, ?)`
	_, err := db.Exec(insertSQL, sessionID, role, content, model)
	return err
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	var openaiReq OpenAIRequest
	if err := json.Unmarshal(body, &openaiReq); err != nil {
		http.Error(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	log.Printf("Session %s - Incoming request with %d messages", globalSetup.Id, len(openaiReq.Messages))
	for _, msg := range openaiReq.Messages {
		cont, err := json.Marshal(msg)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Session %s - User: %s", globalSetup.Id, msg.Content)
		if err := saveMessage(globalSetup.Id, msg.Role, string(cont), openaiReq.Model); err != nil {
			log.Printf("Failed to save message: %v", err)
		}
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
	}

	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Header.Get("Content-Type") == "text/event-stream" {
			return nil
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		var openaiResp OpenAIResponse
		if err := json.Unmarshal(respBody, &openaiResp); err == nil {
			for _, choice := range openaiResp.Choices {
				if choice.Message.Content != "" {
					log.Printf("Session %s - Assistant response: %s", globalSetup.Id, choice.Message.Content)
					if err := saveMessage(globalSetup.Id, "assistant", choice.Message.Content, openaiResp.Model); err != nil {
						log.Printf("Failed to save response: %v", err)
					}
				}
			}
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

	var openaiReq OpenAIRequest
	if err := json.Unmarshal(body, &openaiReq); err != nil {
		http.Error(w, "Invalid JSON request", http.StatusBadRequest)
		return
	}

	log.Printf("Session %s - Incoming request with %d messages", globalSetup.Id, len(openaiReq.Messages))
	for _, msg := range openaiReq.Messages {
		log.Printf("Session %s - User: %s", globalSetup.Id, msg.Content)
		cont, err := json.Marshal(msg)
		if err != nil {
			log.Fatal(err)
		}
		if err := saveMessage(globalSetup.Id, msg.Role, string(cont), openaiReq.Model); err != nil {
			log.Printf("Failed to save message: %v", err)
		}
	}

	target, err := url.Parse(globalSetup.BaseURL)
	if err != nil {
		http.Error(w, "Failed to parse target URL", http.StatusInternalServerError)
		return
	}

	client := &http.Client{}
	req, err := http.NewRequest(r.Method, fmt.Sprintf("%s/v1/chat/completions", target), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to proxy request", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if resp.Header.Get("Content-Type") == "text/event-stream" {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		var assistantContent strings.Builder
		scanner := make([]byte, 4096)

		for {
			n, err := resp.Body.Read(scanner)
			if n > 0 {
				chunk := scanner[:n]
				w.Write(chunk)
				flusher.Flush()

				lines := strings.Split(string(chunk), "\n")
				for _, line := range lines {
					if strings.HasPrefix(line, "data: ") && line != "data: [DONE]" {
						data := strings.TrimPrefix(line, "data: ")
						var streamResp OpenAIResponse
						if json.Unmarshal([]byte(data), &streamResp) == nil {
							for _, choice := range streamResp.Choices {
								if choice.Delta.Content != "" {
									fmt.Print(choice.Delta.Content)
									assistantContent.WriteString(choice.Delta.Content)
								}
							}
						}
					}
				}
			}

			if err == io.EOF {
				break
			}
			if err != nil {
				log.Printf("Error reading stream: %v", err)
				break
			}
		}

		if assistantContent.Len() > 0 {
			fullResponse := assistantContent.String()
			log.Printf("\nSession %s - Assistant response: %s", globalSetup.Id, fullResponse)
			if err := saveMessage(globalSetup.Id, "assistant", fullResponse, openaiReq.Model); err != nil {
				log.Printf("Failed to save streaming response: %v", err)
			}
		}
	} else {
		io.Copy(w, resp.Body)
	}
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

	var openaiReq OpenAIRequest
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
