package main

import (
	"bufio"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/gin-gonic/gin"
)

type User struct {
	Name     string
	pass     string
	IsActive bool
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

	r.Run(":8080")
}
