package main

import (
	"database/sql"
	"encoding/json"
	"errors" // For errors.Is
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings" // Added for worker existence check
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/daos"
	"github.com/pocketbase/pocketbase/models"
	"github.com/pocketbase/pocketbase/models/schema"
	"github.com/pocketbase/pocketbase/tools/types"
	// Cobra is imported by pocketbase.New() implicitly, ensure it's in go.mod
	// _ "github.com/spf13/cobra"
)

// CalendarEntry defines the structure for a single calendar item.
type CalendarEntry struct {
	Date       string `json:"date"`
	WorkerID   string `json:"worker_id,omitempty"`
	WorkerName string `json:"worker_name"`
	Status     string `json:"status"` // "assigned", "queued", "past_done", "past_not_done"
}

// CalendarResponse defines the structure for the calendar API response.
type CalendarResponse struct {
	Assignments       []CalendarEntry `json:"assignments"`
	QueuedAssignments []CalendarEntry `json:"queued_assignments"`
}

const (
	timeLayoutYMD  = "2006-01-02"
	timeLayoutFull = "2006-01-02 15:04:05.000Z" // PocketBase default datetime format (equivalent to types.DateTimeLayout)
)

// AddToQueueRequest defines the structure for the add to queue API request.
type AddToQueueRequest struct {
	WorkerID      string `json:"worker_id"` // Or WorkerName string `json:"worker_name"`
	DurationDays  int    `json:"duration_days"`
	AdminPassword string `json:"admin_password"`
}

// --- Helper Functions ---

func formatDateToYMDGo(t time.Time) string {
	return t.Format(timeLayoutYMD)
}

func getTodayYMDGo() string {
	return formatDateToYMDGo(time.Now().UTC())
}

func parseYMDToGoTime(ymd string) (time.Time, error) {
	return time.Parse(timeLayoutYMD, ymd)
}

func addDaysToYMDGo(ymdString string, days int) (string, error) {
	t, err := parseYMDToGoTime(ymdString)
	if err != nil {
		return "", err
	}
	t = t.AddDate(0, 0, days)
	return formatDateToYMDGo(t), nil
}

func isAdminGo(providedPassword string) bool {
	adminPass := os.Getenv("ADMIN_PASS")
	if adminPass == "" {
		log.Println("Warning: ADMIN_PASS environment variable is not set. Admin actions will be blocked.")
		return false
	}
	return providedPassword == adminPass
}

func logActionGo(dao *daos.Dao, actionType string, details map[string]interface{}) error {
	actionLogCollection, err := dao.FindCollectionByNameOrId("action_log")
	if err != nil {
		log.Printf("Error finding 'action_log' collection for logging: %v", err)
		return fmt.Errorf("failed to find action_log collection: %w", err)
	}

	record := models.NewRecord(actionLogCollection)
	record.Set("action_type", actionType)
	record.Set("timestamp", time.Now().UTC().Format(timeLayoutFull)) // Use timeLayoutFull

	if details != nil {
		detailsJSON, jsonErr := json.Marshal(details)
		if jsonErr != nil {
			log.Printf("Error marshalling details for action log '%s': %v", actionType, jsonErr)
			record.Set("details", fmt.Sprintf(`{"error": "failed to marshal details: %s"}`, jsonErr.Error()))
		} else {
			record.Set("details", string(detailsJSON))
		}
	}

	if err := dao.SaveRecord(record); err != nil {
		log.Printf("Error saving action_log record for action '%s': %v", actionType, err)
		return fmt.Errorf("failed to save action_log record: %w", err)
	}
	return nil
}

func main() {
	app := pocketbase.New()

	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		dao := app.Dao()

		// --- Define Workers Collection ---
		var workersCollection *models.Collection
		existingWorkers, _ := dao.FindCollectionByNameOrId("workers")

		if existingWorkers == nil {
			workersCollection = &models.Collection{
				Name:       "workers",
				Type:       models.CollectionTypeBase,
				ListRule:   nil,
				ViewRule:   nil,
				CreateRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				UpdateRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				DeleteRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				Schema: schema.NewSchema(
					&schema.SchemaField{
						Name:     "name",
						Type:     schema.FieldTypeText,
						Required: true,
						Unique:   true,
						System:   false,
						Options:  &schema.TextOptions{Min: types.Pointer(1), Max: nil, Pattern: ""},
					},
					&schema.SchemaField{
						Name:     "last_assigned_date",
						Type:     schema.FieldTypeDate,
						Required: false,
						System:   false,
						Options:  &schema.DateOptions{},
					},
				),
			}
			if err := dao.SaveCollection(workersCollection); err != nil {
				log.Printf("Error creating 'workers' collection: %v", err)
				return err
			}
			log.Println("'workers' collection created successfully.")
		} else {
			log.Println("'workers' collection already exists.")
			workersCollection = existingWorkers

			rulesChanged := false
			// Ensure ListRule is nil for public access
			if workersCollection.ListRule != nil {
				workersCollection.ListRule = nil
				rulesChanged = true
			}
			// Ensure ViewRule is nil for public access
			if workersCollection.ViewRule != nil {
				workersCollection.ViewRule = nil
				rulesChanged = true
			}

			// Ensure CUD rules are for admin only, consistent with initial creation logic
			// Initial creation uses: types.Pointer("@request.auth.id != '' && @request.auth.admin = true")
			// The instruction uses @request.admin = true. We'll stick to @request.auth.admin for consistency with how it's defined at creation.
			expectedAdminCudRule := types.Pointer("@request.auth.id != '' && @request.auth.admin == true")

			if workersCollection.CreateRule == nil || *workersCollection.CreateRule != *expectedAdminCudRule {
				workersCollection.CreateRule = expectedAdminCudRule
				rulesChanged = true
			}
			if workersCollection.UpdateRule == nil || *workersCollection.UpdateRule != *expectedAdminCudRule {
				workersCollection.UpdateRule = expectedAdminCudRule
				rulesChanged = true
			}
			if workersCollection.DeleteRule == nil || *workersCollection.DeleteRule != *expectedAdminCudRule {
				workersCollection.DeleteRule = expectedAdminCudRule
				rulesChanged = true
			}

			if rulesChanged {
				if err := dao.SaveCollection(workersCollection); err != nil {
					log.Printf("Error saving 'workers' collection with updated rules: %v", err)
					return fmt.Errorf("failed to save workers collection with updated rules: %w", err)
				}
				log.Println("'workers' collection API rules explicitly set/updated for public read and admin CUD.")
			} else {
				log.Println("'workers' collection API rules already conform to public read and admin CUD.")
			}
		}

		if workersCollection == nil || workersCollection.Id == "" {
			log.Println("Critical error: 'workers' collection could not be initialized.")
			return errors.New("workers collection not found and could not be created")
		}

		// --- Define Assignments Collection ---
		existingAssignments, _ := dao.FindCollectionByNameOrId("assignments")
		if existingAssignments == nil {
			assignmentsCollection := &models.Collection{
				Name:       "assignments",
				Type:       models.CollectionTypeBase,
				ListRule:   nil,
				ViewRule:   nil,
				CreateRule: types.Pointer("@request.auth.id != ''"),
				UpdateRule: types.Pointer("@request.auth.id != ''"),
				DeleteRule: types.Pointer("@request.auth.id != ''"),
				Schema: schema.NewSchema(
					&schema.SchemaField{
						Name:     "worker_id",
						Type:     schema.FieldTypeRelation,
						Required: true,
						Options: &schema.RelationOptions{
							CollectionId:  workersCollection.Id,
							CascadeDelete: false,
							MinSelect:     types.Pointer(1),
							MaxSelect:     types.Pointer(1),
						},
					},
					&schema.SchemaField{
						Name:     "date",
						Type:     schema.FieldTypeDate,
						Required: true,
						Unique:   true,
						Options:  &schema.DateOptions{},
					},
					&schema.SchemaField{
						Name:     "status",
						Type:     schema.FieldTypeSelect,
						Required: true,
						Options: &schema.SelectOptions{
							MaxSelect: 1,
							Values:    []string{"assigned", "done", "not_done"},
						},
					},
				),
			}
			if err := dao.SaveCollection(assignmentsCollection); err != nil {
				log.Printf("Error creating 'assignments' collection: %v", err)
				return err
			}
			log.Println("'assignments' collection created successfully.")
		} else {
			log.Println("'assignments' collection already exists.")
		}

		// --- Define Assignment Queue Collection ---
		existingAssignmentQueue, _ := dao.FindCollectionByNameOrId("assignment_queue")
		if existingAssignmentQueue == nil {
			assignmentQueueCollection := &models.Collection{
				Name:       "assignment_queue",
				Type:       models.CollectionTypeBase,
				ListRule:   nil,
				ViewRule:   nil,
				CreateRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				UpdateRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				DeleteRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				Schema: schema.NewSchema(
					&schema.SchemaField{
						Name: "worker_id", Type: schema.FieldTypeRelation, Required: true,
						Options: &schema.RelationOptions{CollectionId: workersCollection.Id, CascadeDelete: false, MinSelect: types.Pointer(1), MaxSelect: types.Pointer(1)},
					},
					&schema.SchemaField{Name: "start_date", Type: schema.FieldTypeDate, Required: true, Options: &schema.DateOptions{}},
					&schema.SchemaField{Name: "duration_days", Type: schema.FieldTypeNumber, Required: true, Options: &schema.NumberOptions{Min: types.Pointer(1.0), Max: types.Pointer(7.0), NoDecimal: true}},
					&schema.SchemaField{Name: "order", Type: schema.FieldTypeNumber, Required: true, Options: &schema.NumberOptions{NoDecimal: true}},
				),
			}
			if err := dao.SaveCollection(assignmentQueueCollection); err != nil {
				log.Printf("Error creating 'assignment_queue' collection: %v", err)
				return err
			}
			log.Println("'assignment_queue' collection created successfully.")
		} else {
			log.Println("'assignment_queue' collection already exists.")
		}

		// --- Define Action Log Collection ---
		existingActionLog, _ := dao.FindCollectionByNameOrId("action_log")
		if existingActionLog == nil {
			actionLogCollection := &models.Collection{
				Name: "action_log", Type: models.CollectionTypeBase,
				ListRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"), ViewRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				CreateRule: types.Pointer("@request.auth.id != ''"), UpdateRule: types.Pointer(""), DeleteRule: types.Pointer(""),
				Schema: schema.NewSchema(
					&schema.SchemaField{Name: "timestamp", Type: schema.FieldTypeDate, Required: true, Options: &schema.DateOptions{}},
					&schema.SchemaField{Name: "action_type", Type: schema.FieldTypeSelect, Required: true, Options: &schema.SelectOptions{MaxSelect: 1, Values: []string{"assigned", "added_to_queue", "marked_not_done", "randomly_assigned", "queue_processed"}}},
					&schema.SchemaField{Name: "details", Type: schema.FieldTypeJson, Required: false, Options: &schema.JsonOptions{}},
				),
			}
			if err := dao.SaveCollection(actionLogCollection); err != nil {
				log.Printf("Error creating 'action_log' collection: %v", err)
				return err
			}
			log.Println("'action_log' collection created successfully.")
		} else {
			log.Println("'action_log' collection already exists.")
		}

		// --- Seed Initial Workers ---
		if workersCollection != nil && workersCollection.Id != "" {
			workerNames := []string{"keromag", "megatorg", "baby-ch"}
			for _, workerName := range workerNames {
				var existingRecord models.Record   // Important to declare it to receive the result
				err := dao.RecordQuery("workers"). // Using dao which is app.Dao()
									AndWhere(dbx.NewExp("LOWER(name) = LOWER({:workerName})", dbx.Params{"workerName": workerName})).
									Limit(1).
									One(&existingRecord) // Use One to fetch into existingRecord

				if err == nil && existingRecord.Id != "" {
					log.Printf("Worker '%s' already exists. Skipping.", workerName)
					continue
				}
				// Check specifically for "no rows" or other "not found" variations
				if err != nil && !(errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "no record found") || strings.Contains(strings.ToLower(err.Error()), "no rows in result set")) {
					log.Printf("Error checking if worker '%s' exists: %v", workerName, err)
					continue
				}
				// If err is sql.ErrNoRows (or similar) or (err == nil && existingRecord.Id is empty), proceed to create
				log.Printf("Worker '%s' does not exist or error was 'no rows'. Creating...", workerName)
				record := models.NewRecord(workersCollection)
				record.Set("name", workerName)
				if errSave := dao.SaveRecord(record); errSave != nil {
					log.Printf("Error seeding worker '%s': %v", workerName, errSave)
				} else {
					log.Printf("Worker '%s' seeded successfully.", workerName)
				}
			}
		} else {
			log.Println("'workers' collection not found or invalid, cannot seed workers.")
		}

		// --- API Routes ---

		// GET /api/dishduty/workers
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/dishduty/workers", // New dedicated endpoint
			Handler: func(c echo.Context) error {
				records, err := app.Dao().FindRecordsByFilter(
					"workers",
					"1=1",   // Get all records
					"+name", // Sort by name ascending
					0,       // No limit (get all)
					0,       // No offset
				)
				if err != nil {
					log.Printf("Error fetching workers for API: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Failed to fetch workers.", err)
				}
				return c.JSON(http.StatusOK, records)
			},
			Middlewares: []echo.MiddlewareFunc{
				// No admin auth middleware here, this is public
			},
		})

		// POST /api/dishduty/queue/add
		e.Router.AddRoute(echo.Route{
			Method: http.MethodPost,
			Path:   "/api/dishduty/queue/add",
			Handler: func(c echo.Context) error {
				var req AddToQueueRequest // Use the new struct type

				if err := c.Bind(&req); err != nil {
					log.Printf("Error binding request for add to queue: %v", err)
					return apis.NewBadRequestError("Invalid request body.", err)
				}

				if !isAdminGo(req.AdminPassword) {
					return apis.NewForbiddenError("Forbidden: Invalid admin password.", nil)
				}

				// Validate DurationDays
				if req.DurationDays < 1 || req.DurationDays > 7 {
					log.Printf("Validation error: duration_days %d out of range", req.DurationDays)
					return apis.NewBadRequestError("duration_days must be between 1 and 7.", nil)
				}

				var worker *models.Record
				var errFindWorker error
				// Note: The AddToQueueRequest struct only has WorkerID. If WorkerName is also needed,
				// the struct and frontend payload should be updated. For now, assuming WorkerID is primary.
				if req.WorkerID != "" {
					worker, errFindWorker = dao.FindRecordById("workers", req.WorkerID)
				} else {
					// If WorkerID is not provided, and WorkerName was an option, this logic would need adjustment.
					// Based on current struct, WorkerID is expected.
					return apis.NewBadRequestError("Bad Request: worker_id is required.", nil)
				}
				if errFindWorker != nil || worker == nil {
					log.Printf("Error finding worker (id: %s): %v", req.WorkerID, errFindWorker)
					return apis.NewNotFoundError("Not Found: Worker not found.", errFindWorker)
				}

				var startDateYMD string
				order := 1
				todayYMD := getTodayYMDGo()

				lastQueueItem, _ := dao.FindFirstRecordByFilter("assignment_queue", "1=1 ORDER BY {{order}} DESC")
				if lastQueueItem != nil {
					lastQueueItemStartDate := lastQueueItem.GetTime("start_date")
					lastQueueItemDuration := lastQueueItem.GetInt("duration_days")
					lastQueueItemEndDate := formatDateToYMDGo(lastQueueItemStartDate.AddDate(0, 0, lastQueueItemDuration-1))
					startDateYMD, _ = addDaysToYMDGo(lastQueueItemEndDate, 1)
					order = lastQueueItem.GetInt("order") + 1
				} else {
					latestAssignment, _ := dao.FindFirstRecordByFilter("assignments", "1=1 ORDER BY date DESC")
					if latestAssignment != nil {
						latestAssignmentDate := latestAssignment.GetTime("date")
						latestAssignmentYMD := formatDateToYMDGo(latestAssignmentDate)
						parsedLatestAssignmentDate, _ := parseYMDToGoTime(latestAssignmentYMD)
						parsedToday, _ := parseYMDToGoTime(todayYMD)
						if parsedLatestAssignmentDate.After(parsedToday) || parsedLatestAssignmentDate.Equal(parsedToday) {
							startDateYMD, _ = addDaysToYMDGo(latestAssignmentYMD, 1)
						} else {
							startDateYMD = todayYMD
						}
					} else {
						startDateYMD = todayYMD
					}
				}

				parsedStartDate, _ := parseYMDToGoTime(startDateYMD)
				parsedToday, _ := parseYMDToGoTime(todayYMD)
				if parsedStartDate.Before(parsedToday) {
					startDateYMD = todayYMD
				}

				finalStartDateForRecord, errParseFinal := time.Parse(timeLayoutYMD, startDateYMD)
				if errParseFinal != nil {
					log.Printf("Error parsing final startDateYMD '%s' for queue: %v", startDateYMD, errParseFinal)
					return apis.NewApiError(http.StatusInternalServerError, "Error formatting start date for DB.", errParseFinal)
				}

				queueCollection, _ := dao.FindCollectionByNameOrId("assignment_queue")
				newQueueRecord := models.NewRecord(queueCollection)
				newQueueRecord.Set("worker_id", worker.Id)
				newQueueRecord.Set("start_date", finalStartDateForRecord.Format(timeLayoutYMD))
				newQueueRecord.Set("duration_days", req.DurationDays) // Use req.DurationDays
				newQueueRecord.Set("order", order)

				if err := dao.SaveRecord(newQueueRecord); err != nil {
					log.Printf("Error saving new queue record: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Could not add worker to queue.", err)
				}
				logActionGo(dao, "added_to_queue", map[string]interface{}{"worker_id": worker.Id, "worker_name": worker.GetString("name"), "duration_days": req.DurationDays, "start_date": startDateYMD, "order": order})
				return c.JSON(http.StatusCreated, map[string]interface{}{"message": "Worker added to queue.", "data": newQueueRecord})
			},
		})

		// GET /api/dishduty/current-assignee
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/dishduty/current-assignee",
			Handler: func(c echo.Context) error {
				if err := ensureDailyAssignmentGo(dao); err != nil {
					log.Printf("Error during ensureDailyAssignmentGo: %v. Attempting to fetch current assignee anyway.", err)
				}

				// Corrected filter for fetching today's assignment
				todayStart := time.Now().UTC().Truncate(24 * time.Hour)
				todayEnd := todayStart.Add(24*time.Hour - 1*time.Nanosecond) // End of the day
				todayYMDForLog := todayStart.Format(timeLayoutYMD)           // For logging if not found

				filter := dbx.NewExp(
					"date >= {:startOfDay} AND date <= {:endOfDay} AND status = 'assigned'",
					dbx.Params{
						"startOfDay": todayStart.UTC().Format(timeLayoutFull),
						"endOfDay":   todayEnd.UTC().Format(timeLayoutFull),
					},
				)
				var assignmentRecord models.Record
				err := dao.RecordQuery("assignments").
					AndWhere(filter).
					Limit(1).
					One(&assignmentRecord)

				if err != nil {
					isNoRowsError := errors.Is(err, sql.ErrNoRows) ||
						strings.Contains(strings.ToLower(err.Error()), "no record found") || // PocketBase specific
						strings.Contains(strings.ToLower(err.Error()), "no rows in result set") // Generic SQL

					if isNoRowsError {
						log.Printf("No current assignment found for today (%s). Returning 404.", todayYMDForLog)
						// Return 404 or a specific structure indicating N/A
						return c.JSON(http.StatusNotFound, map[string]string{"message": "No assignee found for today."})
					}
					log.Printf("Error fetching current assignment for today (%s): %v", todayYMDForLog, err)
					return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to fetch current assignment."})
				}

				// If err is nil, .One() found a record.
				// A check for assignmentRecord.Id == "" might be redundant if .One() always errors on no rows.
				// However, keeping it as a safeguard if some DB driver behaves differently.
				if assignmentRecord.Id == "" {
					log.Printf("No current assignment found for today (%s) (record ID empty after query). Returning 404.", todayYMDForLog)
					// Return 404 or a specific structure indicating N/A
					return c.JSON(http.StatusNotFound, map[string]string{"message": "No assignee found for today."})
				}

				workerID := assignmentRecord.GetString("worker_id")
				assigneeRecord, errWorker := dao.FindRecordById("workers", workerID) // Renamed worker to assigneeRecord for clarity
				if errWorker != nil {
					log.Printf("Error fetching worker details for ID %s: %v", workerID, errWorker)
					return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to fetch worker details."})
				}
				// Calendar route has been moved to the main router setup area (below)

				return c.JSON(http.StatusOK, map[string]interface{}{
					"worker_id":   assigneeRecord.Id,
					"worker_name": assigneeRecord.GetString("name"),
					"date":        assignmentRecord.GetTime("date").Format(timeLayoutYMD),
				})
			},
		})

		// GET /api/dishduty/assignments
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/dishduty/assignments",
			Handler: func(c echo.Context) error {
				startDateStr := c.QueryParam("start_date")
				endDateStr := c.QueryParam("end_date")
				if startDateStr == "" || endDateStr == "" {
					return apis.NewBadRequestError("start_date and end_date query parameters are required.", nil)
				}
				dateRegex := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
				if !dateRegex.MatchString(startDateStr) || !dateRegex.MatchString(endDateStr) {
					return apis.NewBadRequestError("Invalid date format. Use YYYY-MM-DD.", nil)
				}

				startDateTime, _ := time.Parse(timeLayoutYMD, startDateStr)
				endDateTime, _ := time.Parse(timeLayoutYMD, endDateStr)
				endDateTime = endDateTime.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

				records, err := dao.FindRecordsByFilter(
					"assignments",
					"date >= {:startDate} AND date <= {:endDate}",
					"date DESC", 0, 0,
					dbx.Params{
						"startDate": startDateTime.Format(timeLayoutFull),
						"endDate":   endDateTime.Format(timeLayoutFull),
					},
				)
				if err != nil {
					log.Printf("Error fetching assignments: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Failed to fetch assignments.", err)
				}
				result := []map[string]interface{}{}
				for _, record := range records {
					worker, _ := dao.FindRecordById("workers", record.GetString("worker_id"))
					workerName := "Unknown"
					if worker != nil {
						workerName = worker.GetString("name")
					}
					result = append(result, map[string]interface{}{
						"id": record.Id, "worker_name": workerName,
						"date": record.GetTime("date").Format(timeLayoutYMD), "status": record.GetString("status"),
					})
				}
				return c.JSON(http.StatusOK, result)
			},
		})

		// PATCH /api/dishduty/assignments/:id/status
		e.Router.AddRoute(echo.Route{
			Method: http.MethodPatch,
			Path:   "/api/dishduty/assignments/:id/status",
			Handler: func(c echo.Context) error {
				assignmentID := c.PathParam("id")
				requestData := struct {
					Status        string `json:"status"`
					AdminPassword string `json:"admin_password"`
				}{}
				if err := c.Bind(&requestData); err != nil {
					return apis.NewBadRequestError("Failed to parse request data.", err)
				}
				if !isAdminGo(requestData.AdminPassword) {
					return apis.NewForbiddenError("Forbidden: Invalid admin password.", nil)
				}
				validStatuses := map[string]bool{"assigned": true, "done": true, "not_done": true}
				if !validStatuses[requestData.Status] {
					return apis.NewBadRequestError("Invalid status value.", nil)
				}
				assignment, err := dao.FindRecordById("assignments", assignmentID)
				if err != nil {
					return apis.NewNotFoundError("Assignment not found.", err)
				}
				assignment.Set("status", requestData.Status)
				if err := dao.SaveRecord(assignment); err != nil {
					log.Printf("Error updating assignment status: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Failed to update status.", err)
				}
				if requestData.Status == "not_done" {
					workerName := "Unknown"
					worker, _ := dao.FindRecordById("workers", assignment.GetString("worker_id"))
					if worker != nil {
						workerName = worker.GetString("name")
					}
					logActionGo(dao, "marked_not_done", map[string]interface{}{
						"assignment_id": assignment.Id,
						"worker_id":     assignment.GetString("worker_id"),
						"worker_name":   workerName,
						"date":          assignment.GetTime("date").Format(timeLayoutYMD),
					})
				}
				return c.JSON(http.StatusOK, map[string]interface{}{"message": "Assignment status updated."})
			},
		})

		// GET /api/dishduty/action-log
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/dishduty/action-log",
			Handler: func(c echo.Context) error {
				records, err := dao.FindRecordsByFilter("action_log", "1=1", "timestamp DESC", 50, 0)
				if err != nil {
					log.Printf("Error fetching action log: %v", err)
					return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to fetch action log."})
				}
				return c.JSON(http.StatusOK, records)
			},
		})

		// GET /api/dishduty/calendar - MOVED HERE
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/dishduty/calendar",
			Handler: func(c echo.Context) error {
				startDateStr := c.QueryParam("start_date")
				endDateStr := c.QueryParam("end_date")

				if startDateStr == "" || endDateStr == "" {
					return c.JSON(http.StatusBadRequest, map[string]string{"error": "start_date and end_date query parameters are required."})
				}

				dateRegex := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
				if !dateRegex.MatchString(startDateStr) || !dateRegex.MatchString(endDateStr) {
					return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid date format. Use YYYY-MM-DD."})
				}

				responseData := CalendarResponse{
					Assignments:       make([]CalendarEntry, 0),
					QueuedAssignments: make([]CalendarEntry, 0),
				}

				// Fetch actual assignments
				assignmentFilterExp := dbx.NewExp(
					"date >= {:startDate} AND date <= {:endDate}",
					dbx.Params{
						"startDate": startDateStr,
						"endDate":   endDateStr,
					},
				)
				assignmentRecords := []*models.Record{}
				errAssignments := dao.RecordQuery("assignments").
					AndWhere(assignmentFilterExp).
					OrderBy("date DESC").
					All(&assignmentRecords)

				if errAssignments != nil && !errors.Is(errAssignments, sql.ErrNoRows) {
					log.Printf("Error fetching calendar assignments: %v", errAssignments)
					return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to fetch calendar assignments."})
				}

				if errAssignments == nil { // Process if no error or if error is sql.ErrNoRows (records will be empty)
					for _, record := range assignmentRecords {
						worker, _ := dao.FindRecordById("workers", record.GetString("worker_id"))
						workerName := "Unknown"
						if worker != nil {
							workerName = worker.GetString("name")
						}
						// Determine status for calendar display (past_done, past_not_done, assigned)
						assignmentDate := record.GetTime("date")
						today := time.Now().UTC().Truncate(24 * time.Hour)
						status := record.GetString("status")
						calendarStatus := status // Default to actual status

						if assignmentDate.Before(today) {
							if status == "done" {
								calendarStatus = "past_done"
							} else if status == "not_done" || status == "assigned" { // Treat past assigned as not_done for calendar
								calendarStatus = "past_not_done"
							}

						} else if assignmentDate.Equal(today) {
							calendarStatus = status // "assigned", "done", "not_done"
						} else { // Future assignment
							calendarStatus = "assigned" // Future assignments are just "assigned"
						}

						responseData.Assignments = append(responseData.Assignments, CalendarEntry{
							Date:       record.GetTime("date").Format(timeLayoutYMD),
							WorkerID:   record.GetString("worker_id"),
							WorkerName: workerName,
							Status:     calendarStatus,
						})
					}
				}

				// Fetch queued assignments
				// Queued items are relevant if their start_date is within the requested calendar range OR
				// if they don't have a specific end_date but are generally "upcoming".
				// For simplicity, let's fetch queued items whose start_date is before or on the endDateStr of the calendar view.
				// This might need refinement based on how "duration_days" for queued items should affect their visibility in the calendar.
				// For now, we'll list them if their start_date is within the view.
				queuedFilterExp := dbx.NewExp(
					"start_date <= {:endDate}", // Show if it starts before or on the last day of the calendar view
					dbx.Params{"endDate": endDateStr},
				)
				queuedRecords := []*models.Record{}
				errQueued := dao.RecordQuery("assignment_queue").
					AndWhere(queuedFilterExp).
					OrderBy("order ASC"). // Assuming 'order' field exists and is relevant
					All(&queuedRecords)

				if errQueued != nil && !errors.Is(errQueued, sql.ErrNoRows) {
					log.Printf("Error fetching queued assignments: %v", errQueued)
					// Potentially return error or just log and continue with empty queuedAssignments
					// For now, let's log and continue, so assignments can still be shown.
				}

				if errQueued == nil {
					for _, record := range queuedRecords {
						worker, _ := dao.FindRecordById("workers", record.GetString("worker_id"))
						workerName := "Unknown"
						if worker != nil {
							workerName = worker.GetString("name")
						}
						// For queued items, the "date" is their start_date.
						// Status is "queued".
						// Duration could be used to display them over multiple days if the frontend supports it.
						// Here, we just mark the start_date.
						startDate := record.GetTime("start_date").Format(timeLayoutYMD)
						// Optional: consider duration_days if the frontend is to show multi-day queued blocks
						// duration := record.GetInt("duration_days")

						responseData.QueuedAssignments = append(responseData.QueuedAssignments, CalendarEntry{
							Date:       startDate,
							WorkerID:   record.GetString("worker_id"),
							WorkerName: workerName,
							Status:     "queued",
						})
					}
				}
				return c.JSON(http.StatusOK, responseData)
			},
		})

		go func() {
			time.Sleep(3 * time.Second)
			log.Println("Attempting initial daily assignment check after startup...")
			if err := ensureDailyAssignmentGo(dao); err != nil {
				log.Printf("Error during initial ensureDailyAssignmentGo: %v", err)
			}
		}()

		return nil
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// --- Daily Assignment Logic ---
func ensureDailyAssignmentGo(dao *daos.Dao) error {
	log.Println("ensureDailyAssignmentGo: Checking for today's assignment...")
	today := time.Now().UTC()
	todayYMD := today.Format(timeLayoutYMD)
	todayStart := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	// todayStart is: time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	todayEnd := todayStart.Add(24*time.Hour - 1*time.Nanosecond) // End of the day

	// Check for existing assignment for today using a date range
	existingAssignmentFilter := dbx.NewExp(
		"date >= {:startOfDay} AND date <= {:endOfDay}",
		dbx.Params{
			"startOfDay": todayStart.UTC().Format(timeLayoutFull),
			"endOfDay":   todayEnd.UTC().Format(timeLayoutFull),
		},
	)
	var existingAssignment models.Record
	errExisting := dao.RecordQuery("assignments").
		AndWhere(existingAssignmentFilter).
		Limit(1). // We only need one to see if any assignment exists for the day
		One(&existingAssignment)
	// errExisting could be sql.ErrNoRows if no assignment exists for today.

	if errExisting == nil && existingAssignment.Id != "" { // Assignment found for today
		log.Printf("ensureDailyAssignmentGo: Assignment for today (%s) already exists (ID: %s). Status: %s", todayYMD, existingAssignment.Id, existingAssignment.GetString("status"))
		if existingAssignment.GetString("status") == "not_done" {
			log.Printf("ensureDailyAssignmentGo: Today's assignment (%s) was 'not_done'. Deleting to reassign.", todayYMD)
			if err := dao.DeleteRecord(&existingAssignment); err != nil {
				log.Printf("ensureDailyAssignmentGo: Failed to delete 'not_done' assignment %s: %v", existingAssignment.Id, err)
				return fmt.Errorf("failed to delete 'not_done' assignment: %w", err)
			}
		} else {
			return nil
		}
	} else {
		log.Printf("ensureDailyAssignmentGo: No assignment found for today (%s). Proceeding to assign.", todayYMD)
	}

	var workerToAssign *models.Record
	var assignmentSource string

	var dueQueuedAssignment models.Record
	// todayStart is: time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	// For assignment_queue, start_date should be on or before the end of today.
	// Instruction: types.DateTime{Time: todayStartOfDay.Add(23*time.Hour + 59*time.Minute + 59*time.Second)}
	endOfTodayForQueueQuery := todayStart.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

	errQueue := dao.RecordQuery("assignment_queue").
		AndWhere(dbx.NewExp("start_date <= {:effectiveTodayEnd}", dbx.Params{"effectiveTodayEnd": endOfTodayForQueueQuery.UTC().Format(timeLayoutFull)})).
		OrderBy("order ASC").
		Limit(1).
		One(&dueQueuedAssignment)

	if errQueue == nil && dueQueuedAssignment.Id != "" { // Item found and ID is not empty
		workerID := dueQueuedAssignment.GetString("worker_id")
		worker, findErr := dao.FindRecordById("workers", workerID)
		if findErr == nil && worker != nil {
			workerToAssign = worker
			assignmentSource = "queue_processed"
			log.Printf("ensureDailyAssignmentGo: Assigning worker %s (ID: %s) from queue for %s.", worker.GetString("name"), worker.Id, todayYMD)
			// last_assigned_date in workers is FieldTypeDate.
			// todayStart is time.Date(...)
			worker.Set("last_assigned_date", todayStart.Format(timeLayoutYMD))
			if errSaveWorker := dao.SaveRecord(worker); errSaveWorker != nil {
				log.Printf("ensureDailyAssignmentGo: Error updating last_assigned_date for worker %s from queue: %v", worker.GetString("name"), errSaveWorker)
			}
			if errDeleteQueue := dao.DeleteRecord(&dueQueuedAssignment); errDeleteQueue != nil { // Pass pointer to record for deletion
				log.Printf("ensureDailyAssignmentGo: Error deleting queue item %s: %v", dueQueuedAssignment.Id, errDeleteQueue)
			}
		} else {
			log.Printf("ensureDailyAssignmentGo: Error finding worker_id %s from queue item %s: %v.", workerID, dueQueuedAssignment.Id, findErr)
		}
	} else if errQueue != nil && !(errors.Is(errQueue, sql.ErrNoRows) ||
		strings.Contains(strings.ToLower(errQueue.Error()), "no record found") ||
		strings.Contains(strings.ToLower(errQueue.Error()), "no rows in result set")) {
		// Log error only if it's not a "no rows" type of error (or similar "not found" messages)
		log.Printf("ensureDailyAssignmentGo: Error fetching from assignment_queue: %v", errQueue)
	}
	// If sql.ErrNoRows or similar, workerToAssign remains nil, and logic proceeds to random assignment.

	if workerToAssign == nil {
		log.Println("ensureDailyAssignmentGo: No worker from queue. Attempting random assignment.")
		allWorkers, findErr := dao.FindRecordsByFilter("workers", "1=1", "", 0, 0)
		if findErr != nil || len(allWorkers) == 0 {
			log.Printf("ensureDailyAssignmentGo: No workers for random assignment: %v", findErr)
			return fmt.Errorf("no workers available for random assignment: %w", findErr)
		}
		var chosenWorker *models.Record
		var oldestDate time.Time
		firstUnassigned := true

		for _, w := range allWorkers {
			ladStr := w.GetString("last_assigned_date")
			if ladStr == "" {
				chosenWorker = w
				break
			}
			ladTime, parseErr := time.Parse(timeLayoutFull, ladStr)
			if parseErr != nil {
				log.Printf("ensureDailyAssignmentGo: Error parsing last_assigned_date '%s' for worker %s: %v. Skipping.", ladStr, w.GetString("name"), parseErr)
				continue
			}
			if firstUnassigned || ladTime.Before(oldestDate) {
				chosenWorker = w
				oldestDate = ladTime
				firstUnassigned = false
			}
		}
		if chosenWorker == nil && len(allWorkers) > 0 {
			chosenWorker = allWorkers[0]
		}

		if chosenWorker != nil {
			workerToAssign = chosenWorker
			assignmentSource = "randomly_assigned"
			log.Printf("ensureDailyAssignmentGo: Randomly assigning worker %s (ID: %s) for %s.", workerToAssign.GetString("name"), workerToAssign.Id, todayYMD)
			workerToAssign.Set("last_assigned_date", todayStart.Format(timeLayoutFull))
			if err := dao.SaveRecord(workerToAssign); err != nil {
				log.Printf("ensureDailyAssignmentGo: Error updating last_assigned_date for randomly assigned worker %s: %v", workerToAssign.GetString("name"), err)
			}
		} else {
			log.Println("ensureDailyAssignmentGo: No workers available to assign.")
			return fmt.Errorf("no workers available to assign for %s", todayYMD)
		}
	}

	assignmentsCollection, _ := dao.FindCollectionByNameOrId("assignments")
	newAssignment := models.NewRecord(assignmentsCollection)
	newAssignment.Set("worker_id", workerToAssign.Id)
	newAssignment.Set("date", todayStart.Format(timeLayoutYMD))
	newAssignment.Set("status", "assigned")
	if err := dao.SaveRecord(newAssignment); err != nil {
		log.Printf("ensureDailyAssignmentGo: Error saving new assignment for %s on %s: %v", workerToAssign.GetString("name"), todayYMD, err)
		return fmt.Errorf("failed to save new assignment: %w", err)
	}
	log.Printf("ensureDailyAssignmentGo: Assigned worker %s (ID: %s) for %s. Source: %s. ID: %s", workerToAssign.GetString("name"), workerToAssign.Id, todayYMD, assignmentSource, newAssignment.Id)
	logActionGo(dao, "assigned", map[string]interface{}{"worker_id": workerToAssign.Id, "worker_name": workerToAssign.GetString("name"), "date": todayYMD, "source": assignmentSource})
	return nil
}
