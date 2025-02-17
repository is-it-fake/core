package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type EmailRequest struct {
	Email string `json:"email"`
}

// BulkEmailRequest holds the bulk email verification request payload.
type BulkEmailRequest struct {
	Emails []string `json:"emails"`
}

type EmailResponse struct {
	Email   string `json:"email"`
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}

// Check MX records
func checkMXRecords(domain string) (bool, error) {
	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		return false, err
	}
	return len(mxRecords) > 0, nil
}

// mxCacheEntry holds the cached MX records with a timestamp.
type mxCacheEntry struct {
	records   []*net.MX
	timestamp time.Time
}

var (
	// Global cache for MX records.
	mxCache      = make(map[string]mxCacheEntry)
	mxCacheMutex sync.RWMutex
)

// Cache TTL for MX records.
const mxCacheTTL = 5 * time.Minute

// getMXRecordsCached returns MX records for the domain using cache if available.
func getMXRecordsCached(domain string) ([]*net.MX, error) {
	mxCacheMutex.RLock()
	entry, exists := mxCache[domain]
	mxCacheMutex.RUnlock()
	if exists && time.Since(entry.timestamp) < mxCacheTTL {
		return entry.records, nil
	}

	records, err := net.LookupMX(domain)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no MX records found")
	}

	mxCacheMutex.Lock()
	mxCache[domain] = mxCacheEntry{records: records, timestamp: time.Now()}
	mxCacheMutex.Unlock()
	return records, nil
}

// Verify email existence via SMTP
func verifyEmailSMTP(email string) (bool, error) {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid email format")
	}
	domain := parts[1]
	mxValid, err := checkMXRecords(domain)
	if err != nil || !mxValid {
		return false, fmt.Errorf("no valid MX records found")
	}

	mxRecords, _ := net.LookupMX(domain)
	if len(mxRecords) == 0 {
		return false, fmt.Errorf("no MX records found")
	}

	// Connect to the SMTP server
	server := mxRecords[0].Host
	conn, err := net.Dial("tcp", server+":25")
	if err != nil {
		return false, fmt.Errorf("SMTP connection failed")
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, server)
	if err != nil {
		return false, fmt.Errorf("SMTP client initialization failed")
	}
	defer client.Close()

	// Send HELLO
	client.Hello("localhost")

	// Check recipient address
	err = client.Mail("check@yourdomain.com") // Use a valid sender domain
	if err != nil {
		return false, fmt.Errorf("MAIL FROM command failed")
	}

	err = client.Rcpt(email)
	if err != nil {
		return false, fmt.Errorf("RCPT TO command failed")
	}

	return true, nil
}

func checkEmailHandler(c *gin.Context) {
	var req EmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	valid, err := verifyEmailSMTP(req.Email)
	response := EmailResponse{
		Email:   req.Email,
		Valid:   valid,
		Message: "Email exists",
	}

	if err != nil {
		response.Valid = false
		response.Message = err.Error()
	}

	c.JSON(http.StatusOK, response)
}

// checkBulkEmailsStreamHandler handles bulk email verification using SSE,
// concurrency limit, and sends real-time responses.
func checkBulkEmailsStreamHandler(c *gin.Context) {
	var req BulkEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Set SSE headers.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	// Semaphore for concurrency limit (e.g., max 10 goroutines at once).
	concurrencyLimit := 10
	sem := make(chan struct{}, concurrencyLimit)
	var wg sync.WaitGroup
	var writeMutex sync.Mutex

	for _, email := range req.Emails {
		wg.Add(1)
		sem <- struct{}{} // Acquire token.
		go func(mail string) {
			defer wg.Done()
			defer func() { <-sem }() // Release token.

			valid, err := verifyEmailSMTP(mail)
			res := EmailResponse{
				Email:   mail,
				Valid:   valid,
				Message: "Email exists",
			}
			if err != nil {
				res.Valid = false
				res.Message = err.Error()
			}

			// Send SSE event in a thread-safe manner.
			writeMutex.Lock()
			c.SSEvent("emailResult", res)
			c.Writer.Flush()
			writeMutex.Unlock()
		}(email)
	}

	// Wait for all goroutines to finish.
	wg.Wait()

	// Send an "end" event to signal completion.
	writeMutex.Lock()
	c.SSEvent("end", "All emails processed")
	c.Writer.Flush()
	writeMutex.Unlock()
}

// Health check
func health(c *gin.Context) {

	response := struct {
		Status string `json:"status"`
	}{
		Status: "Ok",
	}

	c.JSON(http.StatusOK, response)
}

func main() {
	r := gin.Default()

	// Enable CORS for all origins (unsafe for production)
	r.Use(cors.Default())

	r.GET("/health", health)
	r.POST("/check-email", checkEmailHandler)
	// Bulk email verification endpoint using SSE.
	r.POST("/check-emails", checkBulkEmailsStreamHandler)

	port := ":8080"
	log.Printf("Starting server on %s...", port)
	r.Run(port)
}
