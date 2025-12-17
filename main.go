package main

import (
	"bufio"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type User struct {
	Name     string
	pass     string
	IsActive bool
}

type AccessLog struct {
	Timestamp   time.Time
	ResponseTime int64
	ClientIP    string
	StatusCode  string
	BytesSent   int64
	Method      string
	URL         string
	Username    string
	Hierarchy   string
	ContentType string
}

type UserStats struct {
	Username      string         `json:"username"`
	TotalRequests int            `json:"total_requests"`
	TotalBytes    int64          `json:"total_bytes"`
	LastSeen      time.Time      `json:"last_seen"`
	TopDomains    []DomainStat   `json:"top_domains"`
}

type DomainStat struct {
	Domain   string `json:"domain"`
	Requests int    `json:"requests"`
	Bytes    int64  `json:"bytes"`
}

func BasicAuth(adminPassword string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, password, hasAuth := c.Request.BasicAuth()

		if hasAuth && user == "admin" && password == adminPassword {
			c.Next()
			return
		}

		c.Header("WWW-Authenticate", `Basic realm="Authorization Required"`)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
}

func createUser(username, newPassword string) error {
	users, err := readSquidFile("config/squid_passwd")
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.Name == username {
			return errors.New("User already exists")
		}
	}
	cmd := exec.Command("htpasswd", "-b", "config/squid_passwd", username, newPassword)
	return cmd.Run()
}

func saveUserChanges(username, newPassword string) error {
	cmd := exec.Command("htpasswd", "-b", "config/squid_passwd", username, newPassword)
	return cmd.Run()
}

func readSquidFile(filePath string) ([]User, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var users []User
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		isActive := !strings.HasPrefix(line, "#")
		parts := strings.SplitN(line, ":", 2)
		if len(parts) > 0 {
			if !isActive {
				parts[0] = strings.TrimPrefix(parts[0], "#")
			}
			users = append(users, User{Name: parts[0], pass: parts[1], IsActive: isActive})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

func deleteUser(username, filePath string) error {
	users, err := readSquidFile(filePath)
	if err != nil {
		return err
	}

	var updatedUsers []User
	for _, user := range users {
		if user.Name != username {
			updatedUsers = append(updatedUsers, user)
		}
	}

	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, user := range updatedUsers {
		status := ""
		if !user.IsActive {
			status = "#"
		}
		_, err := file.WriteString(status + user.Name + ":" + user.pass + "\n")
		if err != nil {
			return err
		}
	}
	return nil
}

func toggleUser(username, filePath string) error {
	users, err := readSquidFile(filePath)
	if err != nil {
		return err
	}

	for i, user := range users {
		if user.Name == username {
			users[i].IsActive = !user.IsActive
			break
		}
	}

	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, user := range users {
		if !user.IsActive {
			user.Name = "#" + user.Name
		}
		_, err := file.WriteString(user.Name + ":" + user.pass + "\n")
		if err != nil {
			return err
		}
	}
	return nil
}

func parseAccessLog(line string) (*AccessLog, error) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return nil, errors.New("invalid log format")
	}

	timestampFloat, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return nil, err
	}
	timestamp := time.Unix(int64(timestampFloat), 0)

	responseTime, _ := strconv.ParseInt(fields[1], 10, 64)
	bytesSent, _ := strconv.ParseInt(fields[4], 10, 64)

	return &AccessLog{
		Timestamp:    timestamp,
		ResponseTime: responseTime,
		ClientIP:     fields[2],
		StatusCode:   fields[3],
		BytesSent:    bytesSent,
		Method:       fields[5],
		URL:          fields[6],
		Username:     fields[7],
		Hierarchy:    fields[8],
		ContentType:  fields[9],
	}, nil
}

func extractDomain(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return parsedURL.Host
}

func getStatistics(logPath string) (map[string]*UserStats, error) {
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stats := make(map[string]*UserStats)
	domainStats := make(map[string]map[string]*DomainStat)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		logEntry, err := parseAccessLog(scanner.Text())
		if err != nil {
			continue
		}

		username := logEntry.Username
		if username == "-" {
			continue
		}

		if _, exists := stats[username]; !exists {
			stats[username] = &UserStats{
				Username:   username,
				LastSeen:   logEntry.Timestamp,
				TopDomains: []DomainStat{},
			}
			domainStats[username] = make(map[string]*DomainStat)
		}

		userStat := stats[username]
		userStat.TotalRequests++
		userStat.TotalBytes += logEntry.BytesSent
		if logEntry.Timestamp.After(userStat.LastSeen) {
			userStat.LastSeen = logEntry.Timestamp
		}

		domain := extractDomain(logEntry.URL)
		if domain != "" {
			if _, exists := domainStats[username][domain]; !exists {
				domainStats[username][domain] = &DomainStat{Domain: domain}
			}
			domainStats[username][domain].Requests++
			domainStats[username][domain].Bytes += logEntry.BytesSent
		}
	}

	for username, userStat := range stats {
		var domains []DomainStat
		for _, domainStat := range domainStats[username] {
			domains = append(domains, *domainStat)
		}
		sort.Slice(domains, func(i, j int) bool {
			return domains[i].Requests > domains[j].Requests
		})
		if len(domains) > 10 {
			domains = domains[:10]
		}
		userStat.TopDomains = domains
	}

	return stats, scanner.Err()
}

func main() {
	r := gin.Default()
	r.LoadHTMLGlob("templates/*")
	r.Static("templates", "./templates")

	adminPassword := os.Getenv("SQUID_PASSWORD")
	if adminPassword == "" {
		log.Fatal("SQUID_PASSWORD environment variable not set")
	}

	r.Use(BasicAuth(adminPassword))

	r.GET("/", func(c *gin.Context) {
		users, err := readSquidFile("config/squid_passwd")
		if err != nil {
			c.String(http.StatusInternalServerError, "Ошибка чтения файла: %s", err)
			return
		}

		c.HTML(http.StatusOK, "clients.html", gin.H{"users": users})
	})

	r.GET("/statistics", func(c *gin.Context) {
		c.HTML(http.StatusOK, "statistics.html", nil)
	})

	r.GET("/logout", func(c *gin.Context) {
		c.Header("WWW-Authenticate", `Basic realm="Authorization Required"`)
		c.AbortWithStatus(http.StatusUnauthorized)
	})

	r.POST("/toggle/:name", func(c *gin.Context) {
		name := c.Param("name")
		err := toggleUser(name, "config/squid_passwd")
		if err != nil {
			c.String(http.StatusInternalServerError, "Ошибка изменения пользователя: %s", err)
			return
		}
		c.Status(http.StatusOK)
	})

	r.POST("/delete/:name", func(c *gin.Context) {
		name := c.Param("name")
		err := deleteUser(name, "config/squid_passwd")
		if err != nil {
			c.String(http.StatusInternalServerError, "Ошибка удаления пользователя: %s", err)
			return
		}
		c.Status(http.StatusOK)
	})

	r.POST("/saveUser", func(c *gin.Context) {
		var userData struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.BindJSON(&userData); err != nil {
			c.String(http.StatusBadRequest, "Bad request: %s", err)
			return
		}

		err := saveUserChanges(userData.Username, userData.Password)
		if err != nil {
			c.String(http.StatusInternalServerError, "Error saving user changes: %s", err)
			return
		}

		c.Status(http.StatusOK)
	})

	r.POST("/createUser", func(c *gin.Context) {
		var userData struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.BindJSON(&userData); err != nil {
			c.String(http.StatusBadRequest, "Bad request: %s", err)
			return
		}

		err := createUser(userData.Username, userData.Password)
		if err != nil {
			if err.Error() == "User already exists" {
				c.String(http.StatusConflict, "User already exists")
			} else {
				c.String(http.StatusInternalServerError, "Error creating user: %s", err)
			}
			return
		}

		c.Status(http.StatusOK)
	})

	r.GET("/api/statistics", func(c *gin.Context) {
		stats, err := getStatistics("/var/log/squid/access.log")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"stats": []UserStats{}})
			return
		}

		var statsList []UserStats
		for _, stat := range stats {
			statsList = append(statsList, *stat)
		}

		sort.Slice(statsList, func(i, j int) bool {
			return statsList[i].TotalRequests > statsList[j].TotalRequests
		})

		c.JSON(http.StatusOK, gin.H{"stats": statsList})
	})

	r.GET("/api/statistics/:username", func(c *gin.Context) {
		username := c.Param("username")
		stats, err := getStatistics("/var/log/squid/access.log")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get statistics"})
			return
		}

		userStat, exists := stats[username]
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "User statistics not found"})
			return
		}

		c.JSON(http.StatusOK, userStat)
	})

	r.Run(":8080")
}
