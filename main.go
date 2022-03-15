package main

import (
	"net/http"
	"os"
	"time"

	"github.com/devnull-twitch/teamfactory-manager/lib/k8s"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func main() {
	requestChannel := k8s.Worker()

	r := gin.Default()
	r.SetTrustedProxies(nil)

	group := r.Group("/team_factory")
	{
		// Return port and code for next free server
		group.POST("/game", func(c *gin.Context) {
			respChan := make(chan k8s.PortResponse)
			requestChannel <- k8s.Request{Action: k8s.GetFreeAction, PortResp: respChan}

			select {
			case respData := <-respChan:
				c.JSON(http.StatusOK, struct {
					IP   string `json:"ip"`
					Port int    `json:"port"`
					Code string `json:"code"`
				}{
					IP:   os.Getenv("EXTERNAL_IP"),
					Port: respData.Port,
					Code: respData.LobbyCode,
				})
			case <-time.After(time.Second * 2):
				logrus.Error("new server response timed out")
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		})

		// Get Port by code ?code=ABC
		group.GET("/game", func(c *gin.Context) {
			respChan := make(chan k8s.PortResponse)
			requestChannel <- k8s.Request{
				Action:    k8s.GetLobbyAction,
				LobbyCode: c.Query("code"),
				PortResp:  respChan,
			}

			select {
			case respData := <-respChan:
				c.JSON(http.StatusOK, struct {
					IP   string `json:"ip"`
					Port int    `json:"port"`
				}{
					IP:   os.Getenv("EXTERNAL_IP"),
					Port: respData.Port,
				})
			case <-time.After(time.Second * 2):
				logrus.Error("new server response timed out")
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		})
	}

	r.Run(os.Getenv("WEBSERVER_BIND"))
}
