package http

import (
	"github.com/gin-gonic/gin"

	cardhandlers "sekai-master-api/internal/transport/http/handlers/cards"
	eventhandlers "sekai-master-api/internal/transport/http/handlers/events"
	musichandlers "sekai-master-api/internal/transport/http/handlers/musics"
	systemhandlers "sekai-master-api/internal/transport/http/handlers/system"
	virtuallivehandlers "sekai-master-api/internal/transport/http/handlers/virtuallives"
)

func registerPublicRoutes(
	v1 *gin.RouterGroup,
	healthHandler *systemhandlers.HealthHandler,
	versionsHandler *systemhandlers.VersionsHandler,
	cardHandler *cardhandlers.CardHandler,
	musicHandler *musichandlers.MusicHandler,
	eventHandler *eventhandlers.EventHandler,
	virtualLiveHandler *virtuallivehandlers.VirtualLiveHandler,
) {
	v1.GET("/health", healthHandler.Check)
	v1.GET("/versions/:region", versionsHandler.ByRegion)
	v1.GET("/cards/regions/:id/availability", cardHandler.AvailableRegionsByID)
	v1.GET("/cards/:region/list", cardHandler.List)
	v1.GET("/cards/:region/search", cardHandler.SearchByPrefix)
	v1.GET("/cards/:region/:id", cardHandler.ByID)
	v1.GET("/cards/:region/:id/params", cardHandler.ParamsByID)
	v1.GET("/cards/:region/:id/episodes", cardHandler.EpisodesByID)
	v1.GET("/musics/regions/:id/availability", musicHandler.AvailableRegionsByID)
	v1.GET("/musics/:region/list", musicHandler.List)
	v1.GET("/musics/:region/search", musicHandler.Search)
	v1.GET("/musics/:region/:id", musicHandler.ByID)
	v1.GET("/events/regions/:id/availability", eventHandler.AvailableRegionsByID)
	v1.GET("/events/:region/current", eventHandler.Current)
	v1.GET("/events/:region/list", eventHandler.List)
	v1.GET("/events/:region/search", eventHandler.Search)
	v1.GET("/events/:region/:id", eventHandler.ByID)
	v1.GET("/events/:region/:id/break-times", eventHandler.BreakTimesByID)
	v1.GET("/events/:region/:id/bonuses", eventHandler.BonusesByID)
	v1.GET("/events/:region/:id/cards", eventHandler.CardsByID)
	v1.GET("/events/:region/:id/musics", eventHandler.MusicsByID)
	v1.GET("/events/:region/:id/rewards", eventHandler.RewardsByID)
	v1.GET("/virtualLives/regions/:id/availability", virtualLiveHandler.AvailableRegionsByID)
	v1.GET("/virtualLives/:region/list", virtualLiveHandler.List)
	v1.GET("/virtualLives/:region/search", virtualLiveHandler.Search)
	v1.GET("/virtualLives/:region/:id/items", virtualLiveHandler.ItemsByID)
	v1.GET("/virtualLives/:region/:id/schedules", virtualLiveHandler.SchedulesByID)
	v1.GET("/virtualLives/:region/:id/setlists", virtualLiveHandler.SetlistsByID)
	v1.GET("/virtualLives/:region/:id", virtualLiveHandler.ByID)
}
