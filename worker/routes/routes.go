package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/lavale1012/ss-go-wrkr/handlers"
)

// Register attaches all routes to the router.
func Register(r *gin.Engine) {
	r.GET("/health", handlers.Health)
	r.GET("/ready", handlers.Ready)
	r.POST("/upload", handlers.Upload)
}
