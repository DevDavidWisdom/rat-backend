package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/mdm-system/backend/internal/config"
	"github.com/mdm-system/backend/internal/database"
	"github.com/mdm-system/backend/internal/handlers"
	"github.com/mdm-system/backend/internal/middleware"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
	"github.com/mdm-system/backend/internal/services"
	"github.com/mdm-system/backend/pkg/mqtt"
	"github.com/mdm-system/backend/pkg/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := database.NewDatabase(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Println("Connected to PostgreSQL")

	var mqttClient *mqtt.Client
	mqttClient, err = mqtt.NewClient(&mqtt.Config{
		Broker:   cfg.MQTT.Broker,
		Port:     cfg.MQTT.Port,
		Username: cfg.MQTT.Username,
		Password: cfg.MQTT.Password,
		ClientID: cfg.MQTT.ClientID,
	})
	if err != nil {
		log.Printf("Warning: Failed to create MQTT client: %v", err)
	} else {
		if err := mqttClient.Connect(); err != nil {
			log.Printf("Warning: Failed to connect to MQTT broker: %v", err)
			mqttClient = nil
		} else {
			log.Println("Connected to MQTT broker")
			defer mqttClient.Disconnect()
		}
	}

	userRepo := repository.NewUserRepository(db.Pool)

	// Initialize MinIO
	var minioClient *storage.MinIOClient
	minioClient, err = storage.NewMinIOClient(&storage.MinIOConfig{
		Endpoint:  cfg.MinIO.Endpoint,
		AccessKey: cfg.MinIO.AccessKey,
		SecretKey: cfg.MinIO.SecretKey,
		Bucket:    cfg.MinIO.Bucket,
		UseSSL:    cfg.MinIO.UseSSL,
	})
	if err != nil {
		log.Printf("Warning: Failed to connect to MinIO: %v", err)
	} else {
		log.Println("Connected to MinIO")
	}

	deviceRepo := repository.NewDeviceRepository(db.Pool)
	commandRepo := repository.NewCommandRepository(db.Pool)
	enrollmentRepo := repository.NewEnrollmentRepository(db.Pool)
	policyRepo := repository.NewPolicyRepository(db.Pool)
	groupRepo := repository.NewGroupRepository(db.Pool)
	auditRepo := repository.NewAuditRepository(db.Pool)
	appRepo := repository.NewAppRepository(db.Pool)

	authService := services.NewAuthService(userRepo, cfg)
	auditService := services.NewAuditService(auditRepo)
	mqttPort, _ := strconv.Atoi(cfg.MQTT.Port)
	deviceService := services.NewDeviceService(deviceRepo, enrollmentRepo, authService, cfg.MQTT.Broker, cfg.MQTT.DeviceBroker, mqttPort)
	commandService := services.NewCommandService(commandRepo, deviceRepo, mqttClient)
	policyService := services.NewPolicyService(policyRepo, groupRepo, deviceRepo, commandService)
	appService := services.NewAppService(appRepo, commandService, auditService, minioClient, deviceRepo, cfg)
	geofenceService := services.NewGeofenceService(db.Pool, commandService, auditService)

	telemetryService := services.NewTelemetryService(mqttClient, deviceService, geofenceService)
	if mqttClient != nil {
		go func() {
			if err := telemetryService.Start(context.Background()); err != nil {
				log.Printf("Error starting telemetry service: %v", err)
			}
		}()
	}

	authHandler := handlers.NewAuthHandler(authService)
	deviceHandler := handlers.NewDeviceHandler(deviceService)
	commandHandler := handlers.NewCommandHandler(commandService, deviceRepo)
	policyHandler := handlers.NewPolicyHandler(policyService)
	appHandler := handlers.NewAppHandler(appService)
	auditHandler := handlers.NewAuditHandler(auditService)
	streamingHandler := handlers.NewStreamingHandler(cfg)
	audioHandler := handlers.NewAudioHandler(cfg, commandService, deviceRepo)
	trackingHandler := handlers.NewTrackingHandler(cfg, commandService)
	attendanceHandler := handlers.NewAttendanceHandler(db.Pool, commandService)
	geofenceHandler := handlers.NewGeofenceHandler(db.Pool)
	// Wire attendance result hooks — when device reports results, forward to attendance handler
	commandService.RegisterResultHook("GET_ATTENDANCE", attendanceHandler.HandleCommandResult)
	commandService.RegisterResultHook("CALIBRATE_WIFI", attendanceHandler.HandleCalibrateWiFiResult)

	// Wire device accounts result hook — auto-save extracted emails/phones
	commandService.RegisterResultHook("GET_DEVICE_ACCOUNTS", func(ctx context.Context, cmd *models.Command, result map[string]interface{}) {
		var emails []string
		if rawEmails, exists := result["google_emails"]; exists {
			if arr, ok := rawEmails.([]interface{}); ok {
				for _, e := range arr {
					if s, ok := e.(string); ok {
						emails = append(emails, s)
					}
				}
			}
		}
		var phones []string
		if rawPhones, exists := result["phone_numbers"]; exists {
			if arr, ok := rawPhones.([]interface{}); ok {
				for _, p := range arr {
					if s, ok := p.(string); ok {
						phones = append(phones, s)
					}
				}
			}
		}
		if len(emails) > 0 || len(phones) > 0 {
			if err := deviceRepo.UpdateAccountInfo(ctx, cmd.DeviceID, emails, phones); err != nil {
				log.Printf("Failed to save device account info for device %s: %v", cmd.DeviceID, err)
			}
		}
	})

	// Wire ISSAM extraction result hook — auto-save extracted agent_id
	commandService.RegisterResultHook("EXTRACT_ISSAM", func(ctx context.Context, cmd *models.Command, result map[string]interface{}) {
		if issamID, ok := result["issam_id"].(string); ok && issamID != "" {
			if err := deviceRepo.UpdateIssamID(ctx, cmd.DeviceID, issamID); err != nil {
				log.Printf("Failed to save ISSAM ID for device %s: %v", cmd.DeviceID, err)
			} else {
				log.Printf("Saved ISSAM ID for device %s: %s", cmd.DeviceID, issamID)
			}
		}
	})

	commandService.RegisterResultHook("CAPTURE_ISSAM", func(ctx context.Context, cmd *models.Command, result map[string]interface{}) {
		if issamID, ok := result["issam_id"].(string); ok && issamID != "" {
			if err := deviceRepo.UpdateIssamID(ctx, cmd.DeviceID, issamID); err != nil {
				log.Printf("Failed to save captured ISSAM ID for device %s: %v", cmd.DeviceID, err)
			} else {
				log.Printf("Saved captured ISSAM ID for device %s: %s", cmd.DeviceID, issamID)
			}
		}
	})

	authMiddleware := middleware.NewAuthMiddleware(authService)

	app := fiber.New(fiber.Config{
		AppName:      "MDM Server",
		ReadTimeout:  time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.WriteTimeout) * time.Second,
		BodyLimit:    500 * 1024 * 1024, // 500MB for APK uploads
	})

	app.Use(recover.New())
	app.Use(logger.New(logger.Config{Format: "[${time}] ${status} - ${latency} ${method} ${path}\n"}))
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "*",
		AllowMethods:     "GET,POST,PUT,DELETE,OPTIONS,PATCH",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization",
		AllowCredentials: false,
	}))

	app.Get("/health", func(c *fiber.Ctx) error {
		if err := db.Health(c.Context()); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "unhealthy", "database": "disconnected"})
		}
		return c.JSON(fiber.Map{"status": "healthy", "database": "connected", "mqtt": mqttClient != nil && mqttClient.IsConnected()})
	})

	// ── Public APK download — served from static file baked into Docker image ──
	app.Get("/download/agent", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "application/vnd.android.package-archive")
		c.Set("Content-Disposition", `attachment; filename="mdm-agent.apk"`)
		return c.SendFile("./static/mdm-agent.apk")
	})

	api := app.Group("/api/v1")

	auth := api.Group("/auth")
	auth.Post("/login", authHandler.Login)
	auth.Post("/register", authHandler.Register)
	auth.Post("/refresh", authHandler.RefreshToken)

	// Public device registration - MUST be before protected routes
	api.Post("/enroll", deviceHandler.RegisterDevice)

	protected := api.Group("", authMiddleware.Protected())
	protected.Get("/auth/me", authHandler.Me)

	devices := protected.Group("/devices")
	devices.Get("/", deviceHandler.ListDevices)
	devices.Get("/export", deviceHandler.ExportDevices)
	devices.Get("/stats", deviceHandler.GetStats)
	devices.Get("/:id", deviceHandler.GetDevice)
	devices.Put("/:id", deviceHandler.UpdateDevice)
	devices.Delete("/:id", deviceHandler.DeleteDevice)
	devices.Post("/:id/commands", commandHandler.CreateCommand)
	devices.Get("/:id/commands", commandHandler.ListDeviceCommands)
	devices.Post("/:id/lock", commandHandler.LockDevice)
	devices.Post("/:id/reboot", commandHandler.RebootDevice)
	devices.Post("/:id/wipe", commandHandler.WipeDevice)
	devices.Post("/:id/screenshot", commandHandler.ScreenshotDevice)
	devices.Post("/:id/ping", commandHandler.PingDevice)
	devices.Post("/:id/shell", commandHandler.ExecuteShell)
	devices.Post("/:id/files/list", commandHandler.ListFiles)
	devices.Get("/:id/apps", commandHandler.GetApps)
	devices.Post("/:id/apps/restrictions", commandHandler.SetAppRestrictions)
	devices.Post("/:id/kiosk/start", commandHandler.StartKiosk)
	devices.Post("/:id/kiosk/stop", commandHandler.StopKiosk)
	devices.Post("/:id/wake", commandHandler.WakeDevice)
	devices.Post("/:id/unlock", commandHandler.UnlockDevice)
	devices.Post("/:id/set-password", commandHandler.SetPassword)
	devices.Post("/:id/get-accounts", commandHandler.GetDeviceAccounts)
	devices.Post("/:id/extract-issam", commandHandler.ExtractIssam)
	devices.Post("/:id/capture-issam", commandHandler.CaptureIssam)
	devices.Post("/:id/send-test-notification", commandHandler.SendTestNotification)

	protected.Post("/commands/bulk", commandHandler.CreateBulkCommands)
	protected.Post("/shell/bulk", commandHandler.BulkShell)
	protected.Post("/kiosk/bulk", commandHandler.BulkKiosk)
	protected.Get("/commands/:id", commandHandler.GetCommand)

	enrollments := protected.Group("/enrollments")
	enrollments.Post("/", deviceHandler.CreateEnrollment)
	enrollments.Get("/", deviceHandler.ListEnrollments)
	enrollments.Delete("/:id", deviceHandler.DeactivateEnrollment)

	groups := protected.Group("/groups")
	groups.Get("/", policyHandler.ListGroups)
	groups.Post("/", policyHandler.CreateGroup)

	policies := protected.Group("/policies")
	policies.Get("/", policyHandler.ListPolicies)
	policies.Post("/", policyHandler.CreatePolicy)

	appsGroup := protected.Group("/apps")
	appsGroup.Get("/", appHandler.ListApps)
	appsGroup.Post("/upload", appHandler.UploadAPK)
	appsGroup.Post("/:id/deploy", appHandler.DeployApp)
	appsGroup.Delete("/:id", appHandler.DeleteApp)

	auditGroup := protected.Group("/audit")
	auditGroup.Get("/", auditHandler.ListLogs)

	deviceAPI := api.Group("/agent", authMiddleware.DeviceAuth())
	deviceAPI.Post("/telemetry", deviceHandler.ReportTelemetry)
	deviceAPI.Get("/commands/pending", commandHandler.GetPendingCommands)
	deviceAPI.Post("/commands/:id/result", commandHandler.ReportCommandResult)

	// Streaming (WebRTC signaling)
	streamingHandler.RegisterRoutes(app)
	streamingHandler.RegisterDeviceWS(app)

	// Audio push (admin mic → device speaker)
	audioHandler.RegisterRoutes(app)

	// Live GPS tracking
	trackingHandler.RegisterRoutes(app)

	// Attendance system
	attendanceHandler.RegisterRoutes(protected, deviceAPI)

	// Geofencing
	geofenceHandler.RegisterRoutes(protected)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count, err := deviceService.MarkOfflineDevices(ctx, 3*time.Minute)
				if err != nil {
					log.Printf("Error marking devices offline: %v", err)
				} else if count > 0 {
					log.Printf("Marked %d devices as offline", count)
				}
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := commandService.ProcessTimeouts(ctx); err != nil {
					log.Printf("Error processing command timeouts: %v", err)
				}
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("Shutting down server...")
		cancel()
		_ = app.Shutdown()
	}()

	port := cfg.Server.Port
	if port == "" {
		port = "8080"
	}
	log.Printf("Starting server on port %s", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
