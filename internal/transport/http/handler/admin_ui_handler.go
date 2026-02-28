package handler

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed admin_static/*
var adminStaticFS embed.FS

type AdminUIHandler struct {
	assets http.FileSystem
}

func NewAdminUIHandler() *AdminUIHandler {
	subFS, err := fs.Sub(adminStaticFS, "admin_static")
	if err != nil {
		panic(err)
	}

	return &AdminUIHandler{
		assets: http.FS(subFS),
	}
}

func (handler *AdminUIHandler) LoginPage(c *gin.Context) {
	c.FileFromFS("login.html", handler.assets)
}

func (handler *AdminUIHandler) DashboardPage(c *gin.Context) {
	c.FileFromFS("dashboard.html", handler.assets)
}

func (handler *AdminUIHandler) Asset(c *gin.Context) {
	assetPath := strings.TrimPrefix(c.Param("filepath"), "/")
	if assetPath == "" {
		c.Status(http.StatusNotFound)
		return
	}

	c.FileFromFS(assetPath, handler.assets)
}
