package api

import (
	"encoding/json"
	"net/http"
	"quanty_trade/internal/auth"
	"quanty_trade/internal/database"
	"quanty_trade/internal/models"
	"quanty_trade/internal/strategy"

	"github.com/gin-gonic/gin"
)

var stratMgr *strategy.Manager

func SetManager(m *strategy.Manager) {
	stratMgr = m
}

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	if err := database.DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	if !auth.CheckPasswordHash(req.Password, user.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Username, string(user.Role))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  user,
	})
}

type CreateUserRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Role     string `json:"role"`
}

func CreateUser(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hashedPassword, _ := auth.HashPassword(req.Password)
	user := models.User{
		Username: req.Username,
		Password: hashedPassword,
		Role:     models.UserRole(req.Role),
	}

	if err := database.DB.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	c.JSON(http.StatusOK, user)
}

func ListUsers(c *gin.Context) {
	var users []models.User
	database.DB.Find(&users)
	c.JSON(http.StatusOK, users)
}

func ListStrategies(c *gin.Context) {
	c.JSON(http.StatusOK, stratMgr.ListStrategies())
}

func StartStrategy(c *gin.Context) {
	id := c.Param("id")
	if err := stratMgr.StartStrategy(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "started"})
}

func StopStrategy(c *gin.Context) {
	id := c.Param("id")
	if err := stratMgr.StopStrategy(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// Strategy Square (Templates)

func ListTemplates(c *gin.Context) {
	var templates []models.StrategyTemplate
	database.DB.Preload("Author").Where("is_public = ?", true).Find(&templates)
	c.JSON(http.StatusOK, templates)
}

type PublishTemplateRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Path        string `json:"path" binding:"required"`
}

func PublishTemplate(c *gin.Context) {
	var req PublishTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	template := models.StrategyTemplate{
		Name:        req.Name,
		Description: req.Description,
		Path:        req.Path,
		AuthorID:    userID.(uint),
		IsPublic:    true,
	}

	if err := database.DB.Create(&template).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to publish template"})
		return
	}

	c.JSON(http.StatusOK, template)
}

type ReferenceTemplateRequest struct {
	TemplateID uint   `json:"template_id" binding:"required"`
	Name       string `json:"name" binding:"required"`
	Config     string `json:"config"`
}

func ReferenceTemplate(c *gin.Context) {
	var req ReferenceTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, _ := c.Get("user_id")
	instance := models.StrategyInstance{
		ID:         models.GenerateUUID(), // We'll need to add this
		Name:       req.Name,
		TemplateID: req.TemplateID,
		OwnerID:    userID.(uint),
		Config:     req.Config,
		Status:     "stopped",
	}

	if err := database.DB.Create(&instance).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reference template"})
		return
	}

	// Add to in-memory manager
	var config map[string]interface{}
	json.Unmarshal([]byte(instance.Config), &config)

	// Fetch template to get the path
	var template models.StrategyTemplate
	database.DB.First(&template, instance.TemplateID)

	stratMgr.AddStrategy(instance.ID, instance.Name, template.Path, config)

	c.JSON(http.StatusOK, instance)
}
