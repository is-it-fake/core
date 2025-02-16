package main

import (
	"fmt"
	"log"
	"net/http"
	"net"
	"net/smtp"
	"strings"

	"github.com/gin-gonic/gin"
)

type EmailRequest struct {
	Email string `json:"email"`
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

	// Send HELO
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
		Email: req.Email,
		Valid: valid,
		Message: "Email exists",
	}

	if err != nil {
		response.Valid = false
		response.Message = err.Error()
	}

	c.JSON(http.StatusOK, response)
}

func main() {
	r := gin.Default()
	r.POST("/check-email", checkEmailHandler)

	port := ":8080"
	log.Printf("Starting server on %s...", port)
	r.Run(port)
}
