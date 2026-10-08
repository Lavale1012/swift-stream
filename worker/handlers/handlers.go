package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func Upload(c *gin.Context) {
	// Implementation for upload handler
}

// Health reports that the process is up and serving requests.
func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready reports that the worker can accept work. It has no dependencies to
// check yet, so it always succeeds.
func Ready(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
