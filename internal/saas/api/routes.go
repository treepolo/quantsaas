package api

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"quantsaas/internal/saas/auth"
	"quantsaas/internal/saas/computetask"
	"quantsaas/internal/saas/config"
	controlresearchsvc "quantsaas/internal/saas/controlresearch"
	dynamicparamsvc "quantsaas/internal/saas/dynamicparam"
	"quantsaas/internal/saas/epoch"
	"quantsaas/internal/saas/instance"
	klineinversesvc "quantsaas/internal/saas/klineinverse"
	"quantsaas/internal/saas/marketdata"
	parameterresearchsvc "quantsaas/internal/saas/parameterresearch"
	perturbationsvc "quantsaas/internal/saas/perturbation"
	robustnesssvc "quantsaas/internal/saas/robustness"
	saasstore "quantsaas/internal/saas/store"
	"quantsaas/internal/strategies/sigmoiddca"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentStatusProvider interface {
	IsAgentConnected(userID uint) bool
}

type RouterDeps struct {
	Config            config.Config
	DB                *gorm.DB
	Redis             *saasstore.RedisClient
	Auth              *auth.Service
	InstanceManager   *instance.Manager
	EpochService      *epoch.Service
	EvolutionHandler  *EvolutionHandler
	ComputeTasks      *computetask.Service
	MarketData        *marketdata.Service
	Robustness        *robustnesssvc.Service
	DynamicParameters *dynamicparamsvc.Service
	ParameterResearch *parameterresearchsvc.Service
	ControlResearch   *controlresearchsvc.Service
	KlineInverse      *klineinversesvc.Service
	Perturbation      *perturbationsvc.Service
	AgentStatus       AgentStatusProvider
	WSHandler         gin.HandlerFunc
}

func NewRouter(deps RouterDeps) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	v1 := router.Group("/api/v1")
	v1.POST("/auth/register", registerHandler(deps))
	v1.POST("/auth/login", loginHandler(deps))

	secured := v1.Group("")
	secured.Use(jwtMiddleware(deps.Auth))
	secured.GET("/auth/me", authMeHandler(deps))
	secured.GET("/system/status", systemStatusHandler(deps))
	secured.GET("/strategies", strategiesHandler)
	secured.GET("/strategies/:id", strategyDetailHandler)
	secured.GET("/instances", listInstancesHandler(deps))
	secured.POST("/instances", createInstanceHandler(deps))
	secured.PATCH("/instances/:id", patchInstanceHandler(deps))
	secured.POST("/instances/:id/start", startInstanceHandler(deps))
	secured.POST("/instances/:id/stop", stopInstanceHandler(deps))
	secured.DELETE("/instances/:id", deleteInstanceHandler(deps))
	secured.GET("/instances/:id/lots", listLotsHandler(deps))
	secured.GET("/instances/:id/trades", listTradesHandler(deps))
	secured.GET("/dashboard", dashboardHandler(deps))
	secured.GET("/dashboard/equity-snapshots", equitySnapshotsHandler(deps))
	secured.GET("/dashboard/portfolio", portfolioSummaryHandler(deps))
	secured.GET("/agents/status", agentStatusHandler(deps))

	lab := secured.Group("")
	lab.Use(labOnlyMiddleware(deps.Config.AppRole))
	ev := deps.EvolutionHandler
	if ev == nil {
		ev = NewEvolutionHandler(deps.Config.AppRole, deps.DB, deps.Redis, deps.EpochService)
	}
	bt := NewBacktestHandler(deps.Config.AppRole, deps.DB)
	performanceReports := NewPerformanceReportHandler(deps.DB)
	md := NewMarketDataHandler(deps.Config.AppRole, deps.DB)
	if deps.MarketData != nil {
		md = NewMarketDataHandlerWithService(deps.Config.AppRole, deps.MarketData)
	}
	research := NewResearchStatusHandler(deps.DB)
	researchData := NewResearchDataHandler(deps.Config.AppRole, deps.DB)
	robustnessStudies := deps.Robustness
	if robustnessStudies == nil {
		robustnessStudies = robustnesssvc.NewService(deps.DB, deps.ComputeTasks)
	}
	robustnessHandler := NewRobustnessHandler(robustnessStudies)
	dynamicParameters := deps.DynamicParameters
	if dynamicParameters == nil {
		dynamicParameters = dynamicparamsvc.NewService(deps.DB, deps.ComputeTasks)
	}
	dynamicParameterHandler := NewDynamicParameterHandler(dynamicParameters)
	parameterResearch := deps.ParameterResearch
	if parameterResearch == nil {
		parameterResearch = parameterresearchsvc.NewService(deps.DB, deps.ComputeTasks, robustnessStudies)
	}
	parameterResearchHandler := NewParameterResearchHandler(parameterResearch)
	controlResearch := deps.ControlResearch
	if controlResearch == nil {
		controlResearch = controlresearchsvc.NewService(deps.DB, deps.ComputeTasks, parameterResearch)
	}
	controlResearchHandler := NewControlResearchHandler(controlResearch)
	klineInverse := deps.KlineInverse
	if klineInverse == nil {
		klineInverse = klineinversesvc.NewService(deps.DB, deps.ComputeTasks, nil, parameterResearch)
	}
	klineInverseHandler := NewKlineInverseHandler(klineInverse, deps.DB)
	perturbationStudies := deps.Perturbation
	if perturbationStudies == nil {
		perturbationStudies = perturbationsvc.NewService(deps.DB, deps.ComputeTasks, parameterResearch)
	}
	perturbationHandler := NewPerturbationHandler(perturbationStudies)
	lab.GET("/evolution/tasks", ev.ListTasks)
	lab.POST("/evolution/tasks", ev.CreateTask)
	lab.POST("/evolution/tasks/compute-estimate", ev.EstimateCompute)
	lab.GET("/evolution/tasks/:taskID/trace", ev.GetTrace)
	lab.PATCH("/evolution/tasks/:taskID/trace-mode", ev.SetTraceMode)
	lab.POST("/evolution/tasks/:taskID/cancel", ev.CancelTask)
	lab.POST("/evolution/tasks/:taskID/promote", ev.Promote)
	lab.GET("/evolution/genomes", listGenomesHandler(deps))
	lab.PATCH("/evolution/genomes/:id", ev.UpdateGenome)
	lab.DELETE("/evolution/genomes/:id", ev.DeleteGenome)
	lab.GET("/genome/champion", ev.GetChampion)
	lab.GET("/genome/challengers", listChallengersHandler(deps))
	lab.POST("/backtests", bt.Create)
	lab.GET("/backtests/:id", bt.Get)
	lab.GET("/backtest-results/:id", bt.GetStandardResult)
	lab.GET("/backtest-results/:id/path", bt.GetStandardPathBlock)
	lab.GET("/backtest-results/:id/integrity", bt.VerifyStandardResult)
	lab.POST("/backtest-results/:id/performance-reports", performanceReports.Create)
	lab.GET("/backtest-results/:id/performance-reports", performanceReports.ListForResult)
	lab.GET("/performance-reports/:id", performanceReports.Get)
	lab.GET("/performance-reports/:id/charts/:kind", performanceReports.GetChart)
	lab.GET("/performance-reports/:id/integrity", performanceReports.Verify)
	lab.GET("/evolution/genomes/:id/performance-report", performanceReports.LatestForGenome)
	lab.GET("/market-data/instruments", md.Instruments)
	lab.POST("/market-data/instruments", md.UpsertInstrument)
	lab.PATCH("/market-data/instruments/order", md.ReorderInstruments)
	lab.POST("/market-data/instruments/refresh-starts", md.RefreshAllInstrumentStarts)
	lab.POST("/market-data/instruments/:id/refresh-starts", md.RefreshInstrumentStarts)
	lab.DELETE("/market-data/instruments/:id", md.DeleteInstrument)
	lab.GET("/market-data/klines/status", md.Status)
	lab.GET("/market-data/klines/overview", md.Overview)
	lab.GET("/market-data/charts/sources", md.MarketChartSources)
	lab.GET("/market-data/charts/bars", md.MarketChartBars)
	lab.POST("/market-data/klines/import", md.Import)
	lab.POST("/market-data/klines/update-latest", md.UpdateLatest)
	lab.POST("/market-data/generate/leveraged", md.GenerateLeveraged)
	lab.GET("/market-data/perturbations/sources", perturbationHandler.Sources)
	lab.POST("/market-data/perturbations/groups/plan", perturbationHandler.PlanGroup)
	lab.POST("/market-data/perturbations/groups", perturbationHandler.CreateGroup)
	lab.GET("/market-data/perturbations/groups", perturbationHandler.ListGroups)
	lab.GET("/market-data/perturbations/groups/:id", perturbationHandler.GetGroup)
	lab.PATCH("/market-data/perturbations/groups/:id/metadata", perturbationHandler.UpdateGroup)
	lab.POST("/market-data/perturbations/groups/:id/archive", perturbationHandler.ArchiveGroup)
	lab.POST("/market-data/perturbations/groups/:id/variants/plan", perturbationHandler.PlanVariants)
	lab.POST("/market-data/perturbations/groups/:id/variants", perturbationHandler.StartVariants)
	lab.GET("/market-data/perturbations/groups/:id/variants", perturbationHandler.ListVariants)
	lab.GET("/market-data/perturbations/variants/:id", perturbationHandler.GetVariant)
	lab.POST("/market-data/perturbations/variants/:id/verify", perturbationHandler.VerifyVariant)
	lab.POST("/perturbation-tests/plan", perturbationHandler.PlanTest)
	lab.POST("/perturbation-tests", perturbationHandler.CreateTest)
	lab.GET("/perturbation-tests", perturbationHandler.ListTests)
	lab.GET("/perturbation-tests/:id", perturbationHandler.GetTest)
	lab.PATCH("/perturbation-tests/:id/metadata", perturbationHandler.UpdateTest)
	lab.POST("/perturbation-tests/:id/archive", perturbationHandler.ArchiveTest)
	lab.POST("/perturbation-tests/:id/batches/plan", perturbationHandler.PlanBatch)
	lab.POST("/perturbation-tests/:id/batches", perturbationHandler.StartBatch)
	lab.GET("/perturbation-tests/:id/runs", perturbationHandler.Runs)
	lab.GET("/perturbation-tests/:id/analysis-snapshots", perturbationHandler.Snapshots)
	lab.GET("/perturbation-tests/:id/analysis-snapshots/:snapshot_id", perturbationHandler.Snapshot)
	lab.GET("/market-data/recomposition/sources", md.RecompositionSources)
	lab.GET("/market-data/recomposition/source-bars", md.RecompositionSourceBars)
	lab.POST("/market-data/recomposition/preview-tasks", md.CreateRecompositionPreview)
	lab.GET("/market-data/recomposition/plans/:id", md.GetRecompositionPlan)
	lab.GET("/market-data/recomposition/plans/:id/bars", md.RecompositionPreviewBars)
	lab.POST("/market-data/recomposition/generations", md.CreateRecompositionGeneration)
	lab.GET("/market-data/recomposition/generations/:id", md.GetRecompositionGeneration)
	lab.GET("/market-data/series", md.MarketSeries)
	lab.DELETE("/market-data/series/:id", md.ArchiveMarketSeries)
	lab.DELETE("/market-data/versions/:id", md.ArchiveMarketVersion)
	lab.POST("/market-data/maintenance/audit", md.AuditMaintenance)
	lab.POST("/market-data/maintenance/audit/:id", md.AuditMaintenance)
	lab.POST("/market-data/maintenance/repair", md.RepairMaintenance)
	lab.POST("/market-data/maintenance/repair/:id", md.RepairMaintenance)
	lab.GET("/research-datasets", researchData.List)
	lab.POST("/research-datasets", researchData.Create)
	lab.POST("/research-datasets/preview", researchData.Preview)
	lab.GET("/research-datasets/:id", researchData.Get)
	lab.PATCH("/research-datasets/:id", researchData.Update)
	lab.DELETE("/research-datasets/:id", researchData.Delete)
	lab.GET("/research/status", research.Status)
	lab.GET("/robustness/parameters", robustnessHandler.Parameters)
	lab.POST("/robustness/studies/preview", robustnessHandler.Preview)
	lab.POST("/robustness/studies", robustnessHandler.Create)
	lab.POST("/robustness/studies/import", robustnessHandler.Import)
	lab.GET("/robustness/studies", robustnessHandler.List)
	lab.GET("/robustness/studies/:id", robustnessHandler.Get)
	lab.POST("/robustness/studies/:id/analyze", robustnessHandler.Analyze)
	lab.POST("/dynamic-parameters/studies/preview", dynamicParameterHandler.Preview)
	lab.POST("/dynamic-parameters/studies", dynamicParameterHandler.Create)
	lab.GET("/dynamic-parameters/studies", dynamicParameterHandler.List)
	lab.GET("/dynamic-parameters/studies/:id", dynamicParameterHandler.Get)
	lab.POST("/dynamic-parameters/studies/:id/materialize/preview", dynamicParameterHandler.PreviewMaterialize)
	lab.POST("/dynamic-parameters/studies/:id/materialize", dynamicParameterHandler.Materialize)
	lab.GET("/dynamic-parameters/studies/:id/report-blocks/:blockID", dynamicParameterHandler.ReportBlock)
	parameterResearchAPI := lab.Group("/lab/parameter-research")
	parameterResearchAPI.POST("/configurations", parameterResearchHandler.CreateConfiguration)
	parameterResearchAPI.GET("/dynamic-policy-spaces/:id", parameterResearchHandler.DynamicSpace)
	parameterResearchAPI.GET("/configurations", parameterResearchHandler.ListConfigurations)
	parameterResearchAPI.GET("/configurations/:id", parameterResearchHandler.GetConfiguration)
	parameterResearchAPI.GET("/configurations/:id/runs", parameterResearchHandler.ListRuns)
	parameterResearchAPI.POST("/configurations/:id/archive", parameterResearchHandler.ArchiveConfiguration)
	parameterResearchAPI.POST("/configurations/:id/runs/plan", parameterResearchHandler.PlanRun)
	parameterResearchAPI.POST("/configurations/:id/runs", parameterResearchHandler.StartRun)
	parameterResearchAPI.GET("/runs/:id", parameterResearchHandler.GetRun)
	parameterResearchAPI.GET("/runs/:id/points", parameterResearchHandler.ListPoints)
	parameterResearchAPI.GET("/runs/:id/landscape", parameterResearchHandler.GetLandscape)
	parameterResearchAPI.POST("/runs/:id/stages/plan", parameterResearchHandler.PlanStage)
	parameterResearchAPI.POST("/runs/:id/stages", parameterResearchHandler.StartStage)
	parameterResearchAPI.POST("/runs/:id/pause", parameterResearchHandler.PauseRun)
	parameterResearchAPI.POST("/runs/:id/resume", parameterResearchHandler.ResumeRun)
	parameterResearchAPI.POST("/runs/:id/cancel", parameterResearchHandler.CancelRun)
	parameterResearchAPI.POST("/runs/:id/analyses", parameterResearchHandler.Analyze)
	parameterResearchAPI.POST("/runs/:id/surrogates/plan", parameterResearchHandler.PlanSurrogate)
	parameterResearchAPI.GET("/runs/:id/surrogates", parameterResearchHandler.ListSurrogates)
	parameterResearchAPI.POST("/runs/:id/surrogates", parameterResearchHandler.StartSurrogate)
	parameterResearchAPI.GET("/surrogates/:id", parameterResearchHandler.GetSurrogate)
	parameterResearchAPI.POST("/surrogates/:id/proposals", parameterResearchHandler.CreateProposals)
	parameterResearchAPI.GET("/surrogates/:id/proposals", parameterResearchHandler.ListProposals)
	parameterResearchAPI.POST("/analysis-snapshots/:id/candidates/derive", parameterResearchHandler.DeriveCandidates)
	parameterResearchAPI.POST("/points/:id/candidates", parameterResearchHandler.ManualCandidate)
	parameterResearchAPI.GET("/candidates", parameterResearchHandler.ListCandidates)
	parameterResearchAPI.GET("/candidates/:id", parameterResearchHandler.GetCandidate)
	parameterResearchAPI.GET("/candidates/:id/comparison", parameterResearchHandler.CandidateComparison)
	parameterResearchAPI.GET("/candidates/:id/comparison/blocks/:blockID", parameterResearchHandler.CandidateComparisonBlock)
	parameterResearchAPI.POST("/candidates/:id/archive", parameterResearchHandler.ArchiveCandidate)
	parameterResearchAPI.POST("/candidates/:id/export-to-library", parameterResearchHandler.ExportCandidate)
	parameterResearchAPI.POST("/candidates/:id/promote", parameterResearchHandler.PromoteCandidate)
	parameterResearchAPI.PATCH("/candidates/:id/analyses/:kind", parameterResearchHandler.UpdateAnalysisLink)
	parameterResearchAPI.POST("/series", parameterResearchHandler.CreateSeries)
	parameterResearchAPI.GET("/series/:id", parameterResearchHandler.GetSeries)
	parameterResearchAPI.GET("/analysis-snapshots/:id/comparison", parameterResearchHandler.AnalysisComparison)
	parameterResearchAPI.GET("/analysis-snapshots/:id/comparison/blocks/:blockID", parameterResearchHandler.AnalysisComparisonBlock)
	parameterResearchAPI.GET("/series/:id/comparison-snapshots/:snapshotID", parameterResearchHandler.SeriesComparison)
	parameterResearchAPI.GET("/series/:id/comparison-snapshots/:snapshotID/blocks/:blockID", parameterResearchHandler.SeriesComparisonBlock)
	controlResearchAPI := lab.Group("/lab/control-analysis")
	controlResearchAPI.POST("/tasks/preview", controlResearchHandler.Preview)
	controlResearchAPI.POST("/tasks", controlResearchHandler.Create)
	controlResearchAPI.GET("/tasks", controlResearchHandler.List)
	controlResearchAPI.GET("/tasks/:id", controlResearchHandler.Get)
	controlResearchAPI.POST("/tasks/:id/start-next", controlResearchHandler.StartNext)
	controlResearchAPI.POST("/tasks/:id/cancel", controlResearchHandler.Cancel)
	controlResearchAPI.POST("/tasks/:id/retry", controlResearchHandler.Retry)
	controlResearchAPI.POST("/tasks/:id/extensions/preview", controlResearchHandler.PreviewExtension)
	controlResearchAPI.POST("/tasks/:id/extensions", controlResearchHandler.Extend)
	controlResearchAPI.GET("/tasks/:id/snapshots", controlResearchHandler.Snapshots)
	controlResearchAPI.GET("/tasks/:id/snapshots/:snapshotID/detail", controlResearchHandler.Detail)
	controlResearchAPI.GET("/tasks/:id/snapshots/:snapshotID/comparison", controlResearchHandler.Comparison)
	controlResearchAPI.PATCH("/tasks/:id", controlResearchHandler.UpdateMetadata)
	controlResearchAPI.GET("/tasks/:id/delete-impact", controlResearchHandler.DeleteImpact)
	controlResearchAPI.DELETE("/tasks/:id", controlResearchHandler.DeleteTask)
	controlResearchAPI.DELETE("/tasks/:id/path-details", controlResearchHandler.DeletePathDetails)
	controlResearchAPI.GET("/random-batches/:id/records", controlResearchHandler.RandomRecords)
	controlResearchAPI.DELETE("/random-batches/:id", controlResearchHandler.DeleteUnusedBatch)
	controlResearchAPI.GET("/evaluations/:evaluationID/path", controlResearchHandler.PathBlock)
	klineInverseAPI := lab.Group("/kline-inverse")
	klineInverseAPI.GET("/availability", klineInverseHandler.Availability)
	klineInverseAPI.POST("/studies/drafts", klineInverseHandler.CreateDraft)
	klineInverseAPI.GET("/studies", klineInverseHandler.List)
	klineInverseAPI.GET("/studies/:id", klineInverseHandler.Get)
	klineInverseAPI.POST("/studies/:id/plan", klineInverseHandler.Plan)
	klineInverseAPI.POST("/studies/:id/start", klineInverseHandler.Start)
	klineInverseAPI.POST("/studies/:id/start-next", klineInverseHandler.StartNext)
	klineInverseAPI.POST("/studies/:id/archive", klineInverseHandler.Archive)
	klineInverseAPI.POST("/studies/:id/batches/:batchId/cancel", klineInverseHandler.Cancel)
	klineInverseAPI.POST("/studies/:id/batches/:batchId/resume", klineInverseHandler.Resume)
	klineInverseAPI.POST("/studies/:id/search-extensions/plan", klineInverseHandler.PlanExtension)
	klineInverseAPI.POST("/studies/:id/search-extensions/start", klineInverseHandler.StartExtension)
	klineInverseAPI.POST("/studies/:id/probes/plan", klineInverseHandler.PlanProbe)
	klineInverseAPI.POST("/studies/:id/probes/start", klineInverseHandler.StartProbe)
	klineInverseAPI.GET("/studies/:id/overview", klineInverseHandler.Overview)
	klineInverseAPI.GET("/studies/:id/map", klineInverseHandler.Map)
	klineInverseAPI.GET("/studies/:id/paths", klineInverseHandler.Paths)
	klineInverseAPI.GET("/studies/:id/paths/:pathId", klineInverseHandler.Path)
	klineInverseAPI.GET("/studies/:id/paths/:pathId/lineage", klineInverseHandler.Lineage)
	klineInverseAPI.GET("/studies/:id/anchors/:pathId/boundary", klineInverseHandler.Boundary)
	klineInverseAPI.POST("/studies/:id/paths/:pathId/performance-reports", klineInverseHandler.CreatePerformanceReport)
	klineInverseAPI.GET("/studies/:id/comparison", klineInverseHandler.Comparison)
	if deps.ComputeTasks != nil {
		computeTasks := NewComputeTaskHandler(deps.ComputeTasks)
		lab.GET("/compute-tasks/limits", computeTasks.Limits)
		lab.GET("/compute-tasks", computeTasks.List)
		lab.GET("/compute-tasks/:id", computeTasks.Get)
		lab.GET("/compute-tasks/:id/snapshot", computeTasks.Snapshot)
		lab.GET("/compute-tasks/:id/preview", computeTasks.Preview)
		lab.GET("/compute-tasks/:id/items", computeTasks.Items)
		lab.POST("/compute-tasks/:id/start", computeTasks.Start)
		lab.POST("/compute-tasks/:id/cancel", computeTasks.Cancel)
		lab.POST("/compute-tasks/:id/retry", computeTasks.Retry)
		lab.POST("/compute-cache/lookup", computeTasks.CacheLookup)
	}

	if deps.WSHandler != nil {
		router.GET("/ws/agent", deps.WSHandler)
	}
	mountSPA(router, "web-frontend/dist")
	return router
}

func mountSPA(router *gin.Engine, distDir string) {
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/ws/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "route not found"})
			return
		}

		indexPath := filepath.Join(distDir, "index.html")
		if _, err := os.Stat(indexPath); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "frontend dist not found"})
			return
		}

		rel := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if rel != "" && rel != "." {
			requestedPath := filepath.Join(distDir, filepath.FromSlash(rel))
			if isPathInside(distDir, requestedPath) {
				if info, err := os.Stat(requestedPath); err == nil && !info.IsDir() {
					c.File(requestedPath)
					return
				}
			}
		}
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.File(indexPath)
	})
}

func isPathInside(root, candidate string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	if absCandidate == absRoot {
		return true
	}
	prefix := strings.TrimRight(absRoot, string(filepath.Separator)) + string(filepath.Separator)
	return strings.HasPrefix(absCandidate, prefix)
}

func registerHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Email    string `json:"email" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		user := saasstore.User{Email: req.Email, PasswordHash: string(hash), Role: "user", Plan: "free", Status: "active"}
		if err := deps.DB.Create(&user).Error; err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"id": user.ID, "email": user.Email})
	}
}

func loginHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Email    string `json:"email" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		var user saasstore.User
		if err := deps.DB.Where("email = ?", req.Email).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "登入失敗"})
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "登入失敗"})
			return
		}
		token, err := deps.Auth.SignToken(user.ID, user.Role)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token, "user": gin.H{"id": user.ID, "email": user.Email, "role": user.Role}})
	}
}

func authMeHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var user saasstore.User
		if err := deps.DB.First(&user, currentUserID(c)).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": user.ID, "email": user.Email, "role": user.Role})
	}
}

func jwtMiddleware(authService *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if len(header) < 8 || header[:7] != "Bearer " {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		claims, err := authService.ParseToken(header[7:])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("user_id", claims.UserID)
		c.Set("role", claims.Role)
		c.Next()
	}
}

func labOnlyMiddleware(appRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if appRole != config.AppRoleLab && appRole != config.AppRoleDev {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "此路由僅允許 lab/dev 模式"})
			return
		}
		c.Next()
	}
}

func strategiesHandler(c *gin.Context) {
	c.JSON(http.StatusOK, []sigmoiddca.Manifest{sigmoiddca.StrategyManifest()})
}

func strategyDetailHandler(c *gin.Context) {
	if c.Param("id") != sigmoiddca.StrategyID {
		c.JSON(http.StatusNotFound, gin.H{"error": "strategy not found"})
		return
	}
	c.JSON(http.StatusOK, sigmoiddca.StrategyManifest())
}

func listInstancesHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var rows []saasstore.StrategyInstance
		if err := deps.DB.Where("user_id = ?", currentUserID(c)).Order("created_at DESC").Find(&rows).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, rows)
	}
}

func createInstanceHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Name              string  `json:"name" binding:"required"`
			TemplateID        string  `json:"template_id"`
			Symbol            string  `json:"symbol"`
			Exchange          string  `json:"exchange"`
			InitialUSDT       float64 `json:"initial_usdt"`
			MonthlyInjectUSDT float64 `json:"monthly_inject_usdt"`
			ColdSealedBTC     float64 `json:"cold_sealed_btc"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.TemplateID == "" {
			req.TemplateID = sigmoiddca.StrategyID
		}
		if req.Symbol == "" {
			req.Symbol = "BTCUSDT"
		}
		if req.Exchange == "" {
			req.Exchange = "binance"
		}
		cfgRaw, _ := json.Marshal(req)
		inst := saasstore.StrategyInstance{
			UserID:     currentUserID(c),
			TemplateID: req.TemplateID,
			Name:       req.Name,
			Symbol:     req.Symbol,
			Exchange:   req.Exchange,
			Status:     saasstore.InstanceStatusStopped,
			Config:     saasstore.JSONB(cfgRaw),
		}
		err := deps.DB.Transaction(func(tx *gorm.DB) error {
			template := saasstore.StrategyTemplate{
				ID:       sigmoiddca.StrategyID,
				Name:     sigmoiddca.StrategyName,
				Version:  sigmoiddca.StrategyVersion,
				IsSpot:   sigmoiddca.IsSpot,
				Manifest: saasstore.JSONB(`{}`),
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&template).Error; err != nil {
				return err
			}
			if err := tx.Create(&inst).Error; err != nil {
				return err
			}
			portfolio := saasstore.PortfolioState{
				InstanceID:    inst.ID,
				USDTBalance:   req.InitialUSDT,
				ColdSealedBTC: req.ColdSealedBTC,
				TotalEquity:   req.InitialUSDT,
			}
			if err := tx.Create(&portfolio).Error; err != nil {
				return err
			}
			runtimeState := saasstore.RuntimeState{InstanceID: inst.ID, State: saasstore.JSONB("{}")}
			return tx.Create(&runtimeState).Error
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, inst)
	}
}

func patchInstanceHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseIDParam(c)
		if !ok {
			return
		}
		var instance saasstore.StrategyInstance
		if err := deps.DB.Where("id = ? AND user_id = ?", id, currentUserID(c)).First(&instance).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
			return
		}
		var req struct {
			Status string `json:"status"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		switch strings.ToLower(req.Status) {
		case "running", "run", "start":
			if err := deps.InstanceManager.Start(c.Request.Context(), id); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "running"})
		case "stopped", "paused", "stop", "pause":
			if err := deps.InstanceManager.Stop(c.Request.Context(), id); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "stopped"})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported status"})
		}
	}
}

func startInstanceHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseIDParam(c)
		if !ok {
			return
		}
		if err := deps.InstanceManager.Start(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "running"})
	}
}

func stopInstanceHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseIDParam(c)
		if !ok {
			return
		}
		if err := deps.InstanceManager.Stop(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "stopped"})
	}
}

func deleteInstanceHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseIDParam(c)
		if !ok {
			return
		}
		if err := deps.InstanceManager.Delete(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

func listLotsHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseIDParam(c)
		if !ok {
			return
		}
		var rows []saasstore.SpotLot
		if err := deps.DB.Where("instance_id = ?", id).Order("created_at DESC").Find(&rows).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, rows)
	}
}

func listTradesHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseIDParam(c)
		if !ok {
			return
		}
		var rows []saasstore.TradeRecord
		if err := deps.DB.Where("instance_id = ?", id).Order("created_at DESC").Find(&rows).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, rows)
	}
}

func dashboardHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var portfolios []saasstore.PortfolioState
		err := deps.DB.Joins("JOIN strategy_instances ON strategy_instances.id = portfolio_states.instance_id").
			Where("strategy_instances.user_id = ?", currentUserID(c)).
			Find(&portfolios).Error
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var total float64
		for _, p := range portfolios {
			total += p.TotalEquity
		}
		c.JSON(http.StatusOK, gin.H{"total_equity": total, "instances": len(portfolios)})
	}
}

func equitySnapshotsHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID, err := strconv.ParseUint(c.Query("instance_id"), 10, 64)
		if err != nil || instanceID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "instance_id is required"})
			return
		}
		var instance saasstore.StrategyInstance
		if err := deps.DB.Where("id = ? AND user_id = ?", instanceID, currentUserID(c)).First(&instance).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
			return
		}
		var portfolio saasstore.PortfolioState
		_ = deps.DB.Where("instance_id = ?", instanceID).First(&portfolio).Error
		days := rangeDays(c.DefaultQuery("range", "30d"))
		base := portfolio.TotalEquity
		if base <= 0 {
			base = 10000
		}
		now := time.Now().UTC()
		rows := make([]gin.H, 0, days)
		for i := 0; i < days; i++ {
			progress := float64(i) / math.Max(1, float64(days-1))
			seasonal := math.Sin(float64(i)/3.2) * 0.018
			total := base * (0.96 + progress*0.08 + seasonal)
			benchmark := base * (0.95 + progress*0.055 + math.Sin(float64(i)/4.1)*0.01)
			rows = append(rows, gin.H{
				"time":         now.AddDate(0, 0, -(days - i - 1)).Format(time.RFC3339),
				"total_assets": round2(total),
				"benchmark":    round2(benchmark),
			})
		}
		c.JSON(http.StatusOK, rows)
	}
}

func portfolioSummaryHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID, err := strconv.ParseUint(c.Query("instance_id"), 10, 64)
		if err != nil || instanceID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "instance_id is required"})
			return
		}
		var instance saasstore.StrategyInstance
		if err := deps.DB.Where("id = ? AND user_id = ?", instanceID, currentUserID(c)).First(&instance).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
			return
		}
		var portfolio saasstore.PortfolioState
		if err := deps.DB.Where("instance_id = ?", instanceID).First(&portfolio).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "portfolio not found"})
			return
		}
		price := 62000.0
		createdAt := instance.CreatedAt.Format(time.RFC3339)
		var lastDecision any
		if instance.LastTickAt != nil {
			lastDecision = instance.LastTickAt.Format(time.RFC3339)
		}
		c.JSON(http.StatusOK, gin.H{
			"total_assets":     round2(portfolio.TotalEquity),
			"long_term":        round2(portfolio.DeadBTC * price),
			"active_position":  round2(portfolio.FloatBTC * price),
			"available_funds":  round2(portfolio.USDTBalance),
			"sealed_assets":    round2(portfolio.ColdSealedBTC * price),
			"first_run_at":     createdAt,
			"last_decision_at": lastDecision,
			"decisions_count":  0,
			"monthly_trades":   0,
		})
	}
}

func rangeDays(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "7d", "7":
		return 7
	case "90d", "90":
		return 90
	default:
		return 30
	}
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

func systemStatusHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		connected := false
		if deps.AgentStatus != nil {
			connected = deps.AgentStatus.IsAgentConnected(currentUserID(c))
		}
		engineStatus := "paused"
		if connected {
			engineStatus = "running"
		}
		c.JSON(http.StatusOK, gin.H{
			"api_configured":     connected,
			"api_connected":      connected,
			"engine_status":      engineStatus,
			"requires_reconcile": false,
			"app_role":           deps.Config.AppRole,
			"enabled_features":   enabledFeatures(deps.Config.AppRole),
			"agent_version":      "",
		})
	}
}

func enabledFeatures(appRole string) []string {
	base := []string{"dashboard", "agents", "settings"}
	if appRole == config.AppRoleLab || appRole == config.AppRoleDev {
		return append(base, "strategies", "risk", "backtesting")
	}
	return base
}

func agentStatusHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		connected := false
		if deps.AgentStatus != nil {
			connected = deps.AgentStatus.IsAgentConnected(currentUserID(c))
		}
		c.JSON(http.StatusOK, gin.H{"api_connected": connected})
	}
}

func listChallengersHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var rows []saasstore.GeneRecord
		if err := deps.DB.Scopes(geneScopeFromQuery(c)).Where("role = ?", saasstore.GeneRoleChallenger).Order("created_at DESC").Find(&rows).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, rows)
	}
}

func listGenomesHandler(deps RouterDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var rows []saasstore.GeneRecord
		includeInstruments := normalizeQueryList(c, "instrument_id", "instrument_ids")
		excludeInstruments := normalizeQueryList(c, "exclude_instrument_id", "exclude_instrument_ids")
		tagFilters := normalizeQueryList(c, "tag", "tags")
		query := deps.DB.Scopes(geneScopeFromQuery(c))
		if len(includeInstruments) > 0 {
			query = query.Where("instrument_id IN ?", includeInstruments)
		}
		if len(excludeInstruments) > 0 {
			query = query.Where("instrument_id NOT IN ?", excludeInstruments)
		}
		if err := query.Order("created_at DESC").Find(&rows).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		response := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			if len(tagFilters) > 0 && !hasAnyTag(row.Tags, tagFilters) {
				continue
			}
			role := row.Role
			if role == saasstore.GeneRoleChallenger {
				role = "candidate"
			}
			item := geneResponse(row)
			item["role"] = role
			response = append(response, item)
		}
		c.JSON(http.StatusOK, response)
	}
}

func normalizeQueryList(c *gin.Context, keys ...string) []string {
	values := []string{}
	for _, key := range keys {
		for _, value := range c.QueryArray(key) {
			for _, part := range strings.Split(value, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				values = append(values, strings.ToUpper(part))
			}
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func hasAnyTag(raw saasstore.JSONB, filters []string) bool {
	tags := parseStringSlice(raw)
	if len(tags) == 0 {
		return false
	}
	allowed := map[string]bool{}
	for _, filter := range filters {
		allowed[strings.ToUpper(filter)] = true
	}
	for _, tag := range tags {
		if allowed[strings.ToUpper(tag)] {
			return true
		}
	}
	return false
}

func currentUserID(c *gin.Context) uint {
	if value, ok := c.Get("user_id"); ok {
		if id, ok := value.(uint); ok {
			return id
		}
	}
	return 0
}

func parseIDParam(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return uint(id), true
}
