package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
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

const (
	timeLayoutYMD  = "2006-01-02"
	timeLayoutFull = "2006-01-02 15:04:05.000Z" // PocketBase default datetime format
)

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
	record.Set("timestamp", types.NowDateTime()) // PocketBase uses UTC by default

	if details != nil {
		detailsJSON, jsonErr := json.Marshal(details)
		if jsonErr != nil {
			log.Printf("Error marshalling details for action log '%s': %v", actionType, jsonErr)
			// Decide if you want to log without details or return an error
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
				ListRule:   nil,                                                                   // Public list
				ViewRule:   nil,                                                                   // Public view
				CreateRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"), // Admin create
				UpdateRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"), // Admin update
				DeleteRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"), // Admin delete
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
						Options:  &schema.DateOptions{Min: types.DateTime{}, Max: types.DateTime{}},
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
		}

		if workersCollection == nil || workersCollection.Id == "" {
			log.Println("Critical error: 'workers' collection could not be initialized. Aborting schema setup.")
			return apis.NewNotFoundError("Workers collection not found and could not be created.", nil) // Changed to apis.NewNotFoundError
		}

		// --- Define Assignments Collection ---
		existingAssignments, _ := dao.FindCollectionByNameOrId("assignments")
		if existingAssignments == nil {
			assignmentsCollection := &models.Collection{
				Name:       "assignments",
				Type:       models.CollectionTypeBase,
				ListRule:   nil,
				ViewRule:   nil,
				CreateRule: types.Pointer("@request.auth.id != ''"), // Authenticated users
				UpdateRule: types.Pointer("@request.auth.id != ''"), // Authenticated users
				DeleteRule: types.Pointer("@request.auth.id != ''"), // Authenticated users
				Schema: schema.NewSchema(
					&schema.SchemaField{
						Name:     "worker_id",
						Type:     schema.FieldTypeRelation,
						Required: true,
						System:   false,
						Options: &schema.RelationOptions{
							CollectionId:  workersCollection.Id,
							CascadeDelete: false,
							MinSelect:     types.Pointer(1),
							MaxSelect:     types.Pointer(1),
							DisplayFields: nil,
						},
					},
					&schema.SchemaField{
						Name:     "date",
						Type:     schema.FieldTypeDate,
						Required: true,
						Unique:   true, // One assignment record per date globally
						System:   false,
						Options:  &schema.DateOptions{Min: types.DateTime{}, Max: types.DateTime{}},
					},
					&schema.SchemaField{
						Name:     "status",
						Type:     schema.FieldTypeSelect,
						Required: false, // Default "assigned" (first option)
						System:   false,
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
				CreateRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"), // Admin manages queue
				UpdateRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				DeleteRule: types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				Schema: schema.NewSchema(
					&schema.SchemaField{
						Name:     "worker_id",
						Type:     schema.FieldTypeRelation,
						Required: true,
						System:   false,
						Options: &schema.RelationOptions{
							CollectionId:  workersCollection.Id,
							CascadeDelete: false,
							MinSelect:     types.Pointer(1),
							MaxSelect:     types.Pointer(1),
							DisplayFields: nil,
						},
					},
					&schema.SchemaField{
						Name:     "start_date",
						Type:     schema.FieldTypeDate,
						Required: true,
						System:   false,
						Options:  &schema.DateOptions{Min: types.DateTime{}, Max: types.DateTime{}},
					},
					&schema.SchemaField{
						Name:     "duration_days",
						Type:     schema.FieldTypeNumber,
						Required: true,
						System:   false,
						Options: &schema.NumberOptions{
							Min:       types.Pointer(float64(1)),
							Max:       types.Pointer(float64(7)),
							NoDecimal: true, // In v0.19.4, NoDecimal is a direct boolean, not a pointer
						},
					},
					&schema.SchemaField{
						Name:     "order",
						Type:     schema.FieldTypeNumber,
						Required: true,
						System:   false,
						Options:  &schema.NumberOptions{NoDecimal: true}, // In v0.19.4, NoDecimal is a direct boolean
					},
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
				Name:       "action_log",
				Type:       models.CollectionTypeBase,
				ListRule:   types.Pointer("@request.auth.id != '' && @request.auth.admin = true"), // Admin views logs
				ViewRule:   types.Pointer("@request.auth.id != '' && @request.auth.admin = true"),
				CreateRule: types.Pointer("@request.auth.id != ''"), // System/app creates logs (any authenticated identity, could be a service account)
				UpdateRule: types.Pointer(""),                       // Logs are immutable
				DeleteRule: types.Pointer(""),                       // Logs are not deleted
				Schema: schema.NewSchema(
					&schema.SchemaField{
						Name:     "timestamp",
						Type:     schema.FieldTypeDate,
						Required: true,
						System:   false,
						Options:  &schema.DateOptions{Min: types.DateTime{}, Max: types.DateTime{}},
					},
					&schema.SchemaField{
						Name:     "action_type",
						Type:     schema.FieldTypeSelect,
						Required: true,
						System:   false,
						Options: &schema.SelectOptions{
							MaxSelect: 1,
							Values:    []string{"assigned", "added_to_queue", "marked_not_done", "randomly_assigned", "queue_processed"},
						},
					},
					&schema.SchemaField{
						Name:     "details",
						Type:     schema.FieldTypeJson,
						Required: false,
						System:   false,
						Options:  &schema.JsonOptions{}, // In v0.19.4, JsonOptions doesn't have a MaxSize field
					},
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
			for _, name := range workerNames {
				// Check if worker already exists using FindFirstRecordByFilter and dbx.NewExp
				// Using LOWER to maintain case-insensitivity as in the original attempt
				_, err := dao.FindFirstRecordByFilter(workersCollection.Name, "LOWER(name) = LOWER({:name})", dbx.Params{"name": name})

				if err == nil {
					// Record found, worker exists
					log.Printf("Worker '%s' already exists. Skipping.", name)
					continue
				} else if errors.Is(err, sql.ErrNoRows) {
					// Worker does not exist, proceed to create
					record := models.NewRecord(workersCollection)
					record.Set("name", name)
					// last_assigned_date is optional, not setting it.
					if errSave := dao.SaveRecord(record); errSave != nil {
						log.Printf("Error seeding worker '%s': %v", name, errSave)
					} else {
						log.Printf("Worker '%s' seeded successfully.", name)
					}
				} else {
					// Another error occurred
					log.Printf("Error checking if worker '%s' exists during seeding: %v", name, err)
					continue
				}
			}
		} else {
			log.Println("'workers' collection not found or invalid, cannot seed workers.")
		}

		// --- API Routes ---
		// Middleware for admin password check can be implemented per route if password is in body
		// or as a general middleware if it's a header/query param.
		// For now, checking within handlers as per JS logic.

		// POST /api/dishduty/queue/add
		e.Router.AddRoute(echo.Route{
			Method: http.MethodPost,
			Path:   "/api/dishduty/queue/add",
			Handler: func(c echo.Context) error {
				// In v0.19.4, we use echo.Context directly, not core.RouteEvent

				requestData := struct {
					WorkerName    string `json:"worker_name"`
					WorkerID      string `json:"worker_id"`
					DurationDays  int    `json:"duration_days"`
					AdminPassword string `json:"admin_password"`
				}{}

				if err := c.Bind(&requestData); err != nil {
					return apis.NewBadRequestError("Failed to parse request data.", err)
				}

				if !isAdminGo(requestData.AdminPassword) {
					return apis.NewForbiddenError("Forbidden: Invalid admin password.", nil)
				}

				if requestData.DurationDays < 1 || requestData.DurationDays > 7 {
					return apis.NewBadRequestError("Bad Request: duration_days must be between 1 and 7.", nil)
				}

				var worker *models.Record
				var err error

				if requestData.WorkerID != "" {
					worker, err = dao.FindRecordById("workers", requestData.WorkerID)
				} else if requestData.WorkerName != "" {
					worker, err = dao.FindFirstRecordByFilter("workers", "name = {:name}", dbx.Params{"name": requestData.WorkerName})
				} else {
					return apis.NewBadRequestError("Bad Request: worker_id or worker_name is required.", nil)
				}

				if err != nil || worker == nil {
					log.Printf("Error finding worker (id: %s, name: %s): %v", requestData.WorkerID, requestData.WorkerName, err)
					return apis.NewNotFoundError("Not Found: Worker not found.", err)
				}

				var startDateYMD string
				order := 1

				// Find the last item in the queue to determine start_date and order
				lastQueueItem, _ := dao.FindFirstRecordByFilter(
					"assignment_queue",
					"1=1 ORDER BY {{order}} DESC", // Use {{}} for field names if they might conflict with SQL keywords
				)

				todayYMD := getTodayYMDGo()

				if lastQueueItem != nil {
					lastQueueItemStartDateStr := lastQueueItem.GetString("start_date")
					lastQueueItemStartDate, parseErr := time.Parse(timeLayoutFull, lastQueueItemStartDateStr)
					if parseErr != nil {
						log.Printf("Error parsing lastQueueItem start_date '%s': %v", lastQueueItemStartDateStr, parseErr)
						return apis.NewApiError(http.StatusInternalServerError, "Error processing queue dates.", parseErr)
					}

					lastQueueItemDuration := lastQueueItem.GetInt("duration_days")
					// End date of the last queue item is its start_date + duration_days - 1
					lastQueueItemEndDate := formatDateToYMDGo(lastQueueItemStartDate.AddDate(0, 0, lastQueueItemDuration-1))

					startDateYMD, err = addDaysToYMDGo(lastQueueItemEndDate, 1)
					if err != nil {
						log.Printf("Error calculating start date from last queue item: %v", err)
						return apis.NewApiError(http.StatusInternalServerError, "Error calculating start date.", err)
					}
					order = lastQueueItem.GetInt("order") + 1
				} else {
					// If queue is empty, check current assignments
					latestAssignment, _ := dao.FindFirstRecordByFilter(
						"assignments",
						"1=1 ORDER BY date DESC",
					)
					if latestAssignment != nil {
						latestAssignmentDateStr := latestAssignment.GetString("date")
						latestAssignmentDate, parseErr := time.Parse(timeLayoutFull, latestAssignmentDateStr)
						if parseErr != nil {
							log.Printf("Error parsing latestAssignment date '%s': %v", latestAssignmentDateStr, parseErr)
							return apis.NewApiError(http.StatusInternalServerError, "Error processing assignment dates.", parseErr)
						}

						latestAssignmentYMD := formatDateToYMDGo(latestAssignmentDate)
						parsedLatestAssignmentDate, _ := parseYMDToGoTime(latestAssignmentYMD)
						parsedToday, _ := parseYMDToGoTime(todayYMD)

						if parsedLatestAssignmentDate.After(parsedToday) || parsedLatestAssignmentDate.Equal(parsedToday) {
							startDateYMD, err = addDaysToYMDGo(latestAssignmentYMD, 1)
						} else {
							startDateYMD = todayYMD
						}
						if err != nil {
							log.Printf("Error calculating start date from latest assignment: %v", err)
							return apis.NewApiError(http.StatusInternalServerError, "Error calculating start date.", err)
						}
					} else {
						startDateYMD = todayYMD // No assignments, start today
					}
				}

				// Ensure startDateYMD is not in the past relative to today for new queue items
				parsedStartDate, _ := parseYMDToGoTime(startDateYMD)
				parsedToday, _ := parseYMDToGoTime(todayYMD)
				if parsedStartDate.Before(parsedToday) {
					startDateYMD = todayYMD
				}

				queueCollection, err := dao.FindCollectionByNameOrId("assignment_queue")
				if err != nil {
					return apis.NewApiError(http.StatusInternalServerError, "Failed to find assignment_queue collection.", err)
				}

				newQueueRecord := models.NewRecord(queueCollection)
				newQueueRecord.Set("worker_id", worker.Id)

				// Convert YMD string to full datetime string for PocketBase
				startDatePBFormat, err := time.Parse(timeLayoutYMD, startDateYMD)
				if err != nil {
					log.Printf("Error parsing final startDateYMD '%s' to time.Time: %v", startDateYMD, err)
					return apis.NewApiError(http.StatusInternalServerError, "Error formatting start date for DB.", err)
				}
				// In v0.19.4, format the time directly to string
				formattedDate := startDatePBFormat.Format(timeLayoutFull)
				newQueueRecord.Set("start_date", formattedDate)
				newQueueRecord.Set("duration_days", requestData.DurationDays)
				newQueueRecord.Set("order", order)

				if err := dao.SaveRecord(newQueueRecord); err != nil {
					log.Printf("Error saving new queue record: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Could not add worker to queue.", err)
				}

				logDetails := map[string]interface{}{
					"worker_id":     worker.Id,
					"worker_name":   worker.GetString("name"),
					"duration_days": requestData.DurationDays,
					"start_date":    startDateYMD,
					"order":         order,
				}
				if logErr := logActionGo(dao, "added_to_queue", logDetails); logErr != nil {
					// Log the error but don't fail the request because the main action succeeded
					log.Printf("Failed to log 'added_to_queue' action: %v", logErr)
				}

				return c.JSON(http.StatusCreated, map[string]interface{}{
					"message": "Worker added to queue.",
					"data":    newQueueRecord,
				})
			},
		})
		// Note: If ActivityLogger causes issues with your PB version, you might need to remove it or use a different middleware pattern.

		// --- Daily Assignment Logic ---
		// This function processes the assignment queue and assigns a worker if needed
		processDailyAssignments := func() (map[string]interface{}, error) {
			todayYMD := getTodayYMDGo()
			todayFull := todayYMD + " 00:00:00.000Z"

			// 1. Check if anyone is already assigned for today
			// Use todayYMD for date comparison
			existingAssignmentToday, err := dao.FindFirstRecordByFilter(
				"assignments",
				"date = {:today} AND status = 'assigned'",
				dbx.Params{"today": todayYMD},
			)

			if err != nil && err.Error() != "sql: no rows in result set" {
				log.Printf("Error checking for existing assignment: %v", err)
				return nil, fmt.Errorf("failed to check existing assignments: %w", err)
			}

			if existingAssignmentToday != nil {
				workerId := existingAssignmentToday.GetString("worker_id")
				log.Printf("Worker %s already assigned for %s. No action needed.", workerId, todayYMD)
				return map[string]interface{}{
					"message":            "Assignment already exists for today.",
					"assigned_worker_id": workerId,
				}, nil
			}

			// 2. Process the queue
			// Use todayYMD for date comparison
			dueQueueItem, err := dao.FindFirstRecordByFilter(
				"assignment_queue",
				"start_date <= {:today} ORDER BY {{order}} ASC",
				dbx.Params{"today": todayYMD},
			)

			if err != nil && err.Error() != "sql: no rows in result set" {
				log.Printf("Error checking queue: %v", err)
				return nil, fmt.Errorf("failed to check queue: %w", err)
			}

			if dueQueueItem != nil {
				workerId := dueQueueItem.GetString("worker_id")
				duration := dueQueueItem.GetInt("duration_days")
				effectiveStartDate := dueQueueItem.GetString("start_date")

				// Parse to get just the date part
				parsedStartDate, _ := time.Parse(timeLayoutFull, effectiveStartDate)
				effectiveStartDateYMD := formatDateToYMDGo(parsedStartDate)

				// If queue item's start_date is in the past, start assignments from today
				parsedEffectiveDate, _ := parseYMDToGoTime(effectiveStartDateYMD)
				parsedToday, _ := parseYMDToGoTime(todayYMD)
				if parsedEffectiveDate.Before(parsedToday) {
					effectiveStartDateYMD = todayYMD
				}

				assignmentsCollection, err := dao.FindCollectionByNameOrId("assignments")
				if err != nil {
					return nil, fmt.Errorf("failed to find assignments collection: %w", err)
				}

				worker, err := dao.FindRecordById("workers", workerId)
				if err != nil {
					return nil, fmt.Errorf("failed to find worker: %w", err)
				}

				for i := 0; i < duration; i++ {
					assignmentDate, err := addDaysToYMDGo(effectiveStartDateYMD, i)
					if err != nil {
						log.Printf("Error calculating assignment date: %v", err)
						continue
					}
					assignmentDateFull := assignmentDate + " 00:00:00.000Z"

					// Check if an assignment for this worker and date already exists
					// Use assignmentDate (YMD)
					existingAssignment, err := dao.FindFirstRecordByFilter(
						"assignments",
						"worker_id = {:workerId} AND date = {:date}",
						dbx.Params{"workerId": workerId, "date": assignmentDate},
					)

					if err != nil && err.Error() != "sql: no rows in result set" {
						log.Printf("Error checking for existing assignment: %v", err)
						continue
					}

					if existingAssignment != nil {
						if existingAssignment.GetString("status") != "assigned" {
							existingAssignment.Set("status", "assigned") // Ensure it's marked assigned
							if err := dao.SaveRecord(existingAssignment); err != nil {
								log.Printf("Error updating assignment status: %v", err)
							}
						}
						log.Printf("Assignment for %s on %s already exists, status updated if needed.", worker.GetString("name"), assignmentDate)
					} else {
						newAssignment := models.NewRecord(assignmentsCollection)
						newAssignment.Set("worker_id", workerId)
						newAssignment.Set("date", assignmentDateFull)
						newAssignment.Set("status", "assigned")
						if err := dao.SaveRecord(newAssignment); err != nil {
							log.Printf("Error creating assignment: %v", err)
						}
					}
				}

				// Remove from queue
				if err := dao.DeleteRecord(dueQueueItem); err != nil {
					log.Printf("Error deleting queue item: %v", err)
				}

				// Update worker's last_assigned_date
				lastAssignmentDate, err := addDaysToYMDGo(effectiveStartDateYMD, duration-1)
				if err == nil {
					worker.Set("last_assigned_date", lastAssignmentDate+" 00:00:00.000Z")
					if err := dao.SaveRecord(worker); err != nil {
						log.Printf("Error updating worker's last_assigned_date: %v", err)
					}
				}

				logDetails := map[string]interface{}{
					"worker_id":     workerId,
					"worker_name":   worker.GetString("name"),
					"duration_days": duration,
					"start_date":    effectiveStartDateYMD,
				}
				if logErr := logActionGo(dao, "queue_processed", logDetails); logErr != nil {
					log.Printf("Failed to log 'queue_processed' action: %v", logErr)
				}

				log.Printf("Assigned %s for %d days starting %s from queue.", worker.GetString("name"), duration, effectiveStartDateYMD)
				return map[string]interface{}{
					"message":            "Worker assigned from queue.",
					"assigned_worker_id": workerId,
					"duration":           duration,
				}, nil
			}

			// 3. Random assignment if no one assigned and queue is empty or not due
			log.Println("No due queue item. Attempting random assignment.")
			workers, err := dao.FindRecordsByFilter(
				"workers",
				"1=1 ORDER BY last_assigned_date ASC NULLS FIRST, created ASC", // Prioritize those never assigned or oldest last_assigned_date
				"id", 0, 100, // Get all workers, up to a reasonable limit
			)

			if err != nil {
				log.Printf("Error finding workers for random assignment: %v", err)
				return nil, fmt.Errorf("failed to find workers: %w", err)
			}

			if len(workers) == 0 {
				log.Println("No workers available for random assignment.")
				logDetails := map[string]interface{}{
					"reason": "No workers in the system.",
				}
				if logErr := logActionGo(dao, "random_assignment_failed", logDetails); logErr != nil {
					log.Printf("Failed to log 'random_assignment_failed' action: %v", logErr)
				}
				return map[string]interface{}{"error": "No workers available."}, fmt.Errorf("no workers available")
			}

			selectedWorker := workers[0] // Simplest: pick the first one from the sorted list
			assignmentsCollection, err := dao.FindCollectionByNameOrId("assignments")
			if err != nil {
				return nil, fmt.Errorf("failed to find assignments collection: %w", err)
			}

			newAssignment := models.NewRecord(assignmentsCollection)
			newAssignment.Set("worker_id", selectedWorker.Id)
			newAssignment.Set("date", todayFull)
			newAssignment.Set("status", "assigned")
			if err := dao.SaveRecord(newAssignment); err != nil {
				log.Printf("Error creating random assignment: %v", err)
				return nil, fmt.Errorf("failed to create assignment: %w", err)
			}

			selectedWorker.Set("last_assigned_date", todayFull)
			if err := dao.SaveRecord(selectedWorker); err != nil {
				log.Printf("Error updating worker's last_assigned_date: %v", err)
			}

			logDetails := map[string]interface{}{
				"worker_id":   selectedWorker.Id,
				"worker_name": selectedWorker.GetString("name"),
				"date":        todayYMD,
			}
			if logErr := logActionGo(dao, "randomly_assigned", logDetails); logErr != nil {
				log.Printf("Failed to log 'randomly_assigned' action: %v", logErr)
			}

			log.Printf("Randomly assigned %s for %s.", selectedWorker.GetString("name"), todayYMD)
			return map[string]interface{}{
				"message":            "Worker randomly assigned.",
				"assigned_worker_id": selectedWorker.Id,
			}, nil
		}

		// POST /api/dishduty/trigger-daily-assignment
		e.Router.AddRoute(echo.Route{
			Method: http.MethodPost,
			Path:   "/api/dishduty/trigger-daily-assignment",
			Handler: func(c echo.Context) error {
				result, err := processDailyAssignments()
				if err != nil {
					log.Printf("Error in daily assignment trigger: %v", err)
					logDetails := map[string]interface{}{
						"error": err.Error(),
					}
					if logErr := logActionGo(dao, "daily_assignment_error", logDetails); logErr != nil {
						log.Printf("Failed to log 'daily_assignment_error' action: %v", logErr)
					}
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error during daily assignment.", err)
				}
				return c.JSON(http.StatusOK, result)
			},
		})

		// GET /api/dishduty/current-assignee
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/dishduty/current-assignee",
			Handler: func(c echo.Context) error {
				todayYMD := getTodayYMDGo()
				// todayFull := todayYMD + " 00:00:00.000Z" // Unused

				// Use todayYMD for date comparison
				currentAssignment, err := dao.FindFirstRecordByFilter(
					"assignments",
					"date = {:today} AND status = 'assigned'",
					dbx.Params{"today": todayYMD},
				)

				if err != nil && err.Error() != "sql: no rows in result set" {
					log.Printf("Error fetching current assignee: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				if currentAssignment != nil {
					workerId := currentAssignment.GetString("worker_id")
					worker, err := dao.FindRecordById("workers", workerId)
					if err != nil {
						log.Printf("Error fetching worker details: %v", err)
						return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
					}

					return c.JSON(http.StatusOK, map[string]interface{}{
						"date":        todayYMD,
						"worker_id":   worker.Id,
						"worker_name": worker.GetString("name"),
						"status":      currentAssignment.GetString("status"),
					})
				} else {
					// If no direct assignment, try to run daily assignment
					log.Printf("No current assignee for %s, attempting to process daily assignments.", todayYMD)
					processingResult, err := processDailyAssignments()
					if err != nil {
						log.Printf("Error processing daily assignments: %v", err)
						return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
					}

					if workerId, ok := processingResult["assigned_worker_id"].(string); ok && workerId != "" {
						worker, err := dao.FindRecordById("workers", workerId)
						if err != nil {
							log.Printf("Error fetching newly assigned worker details: %v", err)
							return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
						}

						return c.JSON(http.StatusOK, map[string]interface{}{
							"date":        todayYMD,
							"worker_id":   worker.Id,
							"worker_name": worker.GetString("name"),
							"status":      "assigned", // Assumed from processing
							"message":     processingResult["message"],
						})
					}

					return c.JSON(http.StatusNotFound, map[string]interface{}{
						"message": "No worker is currently assigned for today.",
						"details": processingResult,
					})
				}
			},
		})

		// POST /api/dishduty/mark-not-done
		e.Router.AddRoute(echo.Route{
			Method: http.MethodPost,
			Path:   "/api/dishduty/mark-not-done",
			Handler: func(c echo.Context) error {
				requestData := struct {
					Date          string `json:"date"`
					AdminPassword string `json:"admin_password"`
				}{}

				if err := c.Bind(&requestData); err != nil {
					return apis.NewBadRequestError("Failed to parse request data.", err)
				}

				if !isAdminGo(requestData.AdminPassword) {
					return apis.NewForbiddenError("Forbidden: Invalid admin password.", nil)
				}

				if requestData.Date == "" {
					return apis.NewBadRequestError("Bad Request: 'date' (for yesterday's task) is required.", nil)
				}
				yesterdayYMD := requestData.Date // Expecting YYYY-MM-DD
				// yesterdayFull := yesterdayYMD + " 00:00:00.000Z" // Unused
				todayYMD := getTodayYMDGo()
				todayFull := todayYMD + " 00:00:00.000Z" // Used later for newTodayAssignment

				// Find the assignment to mark as not done
				// Use yesterdayYMD for date comparison
				assignmentToMark, err := dao.FindFirstRecordByFilter(
					"assignments",
					"date = {:date}",
					dbx.Params{"date": yesterdayYMD},
				)

				if err != nil {
					if err.Error() == "sql: no rows in result set" {
						return apis.NewNotFoundError(fmt.Sprintf("Not Found: No assignment found for date %s.", yesterdayYMD), nil)
					}
					log.Printf("Error finding assignment to mark: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				originalStatus := assignmentToMark.GetString("status")
				failedWorkerId := assignmentToMark.GetString("worker_id")
				assignmentToMark.Set("status", "not_done")
				if err := dao.SaveRecord(assignmentToMark); err != nil {
					log.Printf("Error updating assignment status: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				failedWorker, err := dao.FindRecordById("workers", failedWorkerId)
				if err != nil {
					log.Printf("Error finding failed worker: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				// Reassign for Today
				// 1. Cancel/remove any existing assignment for today IF it's not the failed worker
				// Use todayYMD for date comparison
				existingTodayAssignment, err := dao.FindFirstRecordByFilter(
					"assignments",
					"date = {:today}",
					dbx.Params{"today": todayYMD},
				)

				if err != nil && err.Error() != "sql: no rows in result set" {
					log.Printf("Error checking for existing today's assignment: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				todayReassigned := false
				if existingTodayAssignment != nil && existingTodayAssignment.GetString("worker_id") != failedWorkerId {
					log.Printf("Cancelling existing assignment for %s on %s to reassign to %s",
						existingTodayAssignment.GetString("worker_id"), todayYMD, failedWorker.GetString("name"))

					if err := dao.DeleteRecord(existingTodayAssignment); err != nil {
						log.Printf("Error deleting existing assignment: %v", err)
						return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
					}

					logDetails := map[string]interface{}{
						"date":                    todayYMD,
						"cancelled_worker_id":     existingTodayAssignment.GetString("worker_id"),
						"reassigned_to_worker_id": failedWorkerId,
					}
					if logErr := logActionGo(dao, "assignment_cancelled_for_reassignment", logDetails); logErr != nil {
						log.Printf("Failed to log 'assignment_cancelled_for_reassignment' action: %v", logErr)
					}
				}

				// 2. Create new assignment for failed_worker_id for today, if not already assigned to them
				if existingTodayAssignment == nil || existingTodayAssignment.GetString("worker_id") != failedWorkerId {
					assignmentsCollection, err := dao.FindCollectionByNameOrId("assignments")
					if err != nil {
						log.Printf("Error finding assignments collection: %v", err)
						return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
					}

					newTodayAssignment := models.NewRecord(assignmentsCollection)
					newTodayAssignment.Set("worker_id", failedWorkerId)
					newTodayAssignment.Set("date", todayFull)
					newTodayAssignment.Set("status", "assigned") // Reassignment is 'assigned'

					if err := dao.SaveRecord(newTodayAssignment); err != nil {
						log.Printf("Error creating new assignment: %v", err)
						return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
					}

					todayReassigned = true

					// Update failed worker's last_assigned_date if this is a new assignment for them today
					failedWorker.Set("last_assigned_date", todayFull)
					if err := dao.SaveRecord(failedWorker); err != nil {
						log.Printf("Error updating worker's last_assigned_date: %v", err)
					}
				}

				// Shift subsequent assignments and queue items by one day
				// 1. Shift future assignments
				futureAssignments, err := dao.FindRecordsByFilter(
					"assignments",
					"date > {:today} ORDER BY date",
					"id", 0, 100, // Required parameters for v0.19.4: sort field, offset, limit
					dbx.Params{"today": todayYMD}, // Corrected to use YMD for date field
				)

				if err != nil && err.Error() != "sql: no rows in result set" {
					log.Printf("Error finding future assignments: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				for _, assign := range futureAssignments {
					originalDateStr := assign.GetString("date")
					originalDate, err := time.Parse(timeLayoutFull, originalDateStr)
					if err != nil {
						log.Printf("Error parsing assignment date: %v", err)
						continue
					}

					newDateYMD, err := addDaysToYMDGo(formatDateToYMDGo(originalDate), 1)
					if err != nil {
						log.Printf("Error calculating new date: %v", err)
						continue
					}

					newDate, err := time.Parse(timeLayoutYMD, newDateYMD)
					if err != nil {
						log.Printf("Error parsing new date: %v", err)
						continue
					}

					assign.Set("date", newDate.Format(timeLayoutFull))
					if err := dao.SaveRecord(assign); err != nil {
						log.Printf("Error updating assignment date: %v", err)
					}
				}

				// 2. Shift queue items
				queueItems, err := dao.FindRecordsByFilter(
					"assignment_queue",
					"1=1 ORDER BY {{order}} ASC", // Process all queue items
					"id", 0, 100,                 // Required parameters for v0.19.4: sort field, offset, limit
					dbx.Params{},
				)

				if err != nil && err.Error() != "sql: no rows in result set" {
					log.Printf("Error finding queue items: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				for _, item := range queueItems {
					originalStartDateStr := item.GetString("start_date")
					originalStartDate, err := time.Parse(timeLayoutFull, originalStartDateStr)
					if err != nil {
						log.Printf("Error parsing queue item start date: %v", err)
						continue
					}

					originalStartDateYMD := formatDateToYMDGo(originalStartDate)
					parsedOriginalDate, _ := parseYMDToGoTime(originalStartDateYMD)
					parsedToday, _ := parseYMDToGoTime(todayYMD)

					// Only shift if its start date is after or on the day the penalty is applied
					if parsedOriginalDate.After(parsedToday) || parsedOriginalDate.Equal(parsedToday) {
						newStartDateYMD, err := addDaysToYMDGo(originalStartDateYMD, 1)
						if err != nil {
							log.Printf("Error calculating new start date: %v", err)
							continue
						}

						newStartDate, err := time.Parse(timeLayoutYMD, newStartDateYMD)
						if err != nil {
							log.Printf("Error parsing new start date: %v", err)
							continue
						}

						item.Set("start_date", newStartDate.Format(timeLayoutFull))
						if err := dao.SaveRecord(item); err != nil {
							log.Printf("Error updating queue item start date: %v", err)
						}
					}
				}

				logDetails := map[string]interface{}{
					"date":               yesterdayYMD,
					"original_status":    originalStatus,
					"failed_worker_id":   failedWorkerId,
					"failed_worker_name": failedWorker.GetString("name"),
					"reassigned_today":   todayReassigned,
					"today_reassigned_to": func() interface{} {
						if todayReassigned {
							return failedWorkerId
						}
						return nil
					}(),
				}
				if logErr := logActionGo(dao, "marked_not_done", logDetails); logErr != nil {
					log.Printf("Failed to log 'marked_not_done' action: %v", logErr)
				}

				return c.JSON(http.StatusOK, map[string]interface{}{
					"message": fmt.Sprintf("Assignment for %s marked 'not_done'. Worker %s has been reassigned for %s if they weren't already. Subsequent assignments shifted.",
						yesterdayYMD, failedWorker.GetString("name"), todayYMD),
				})
			},
		})

		// GET /api/dishduty/calendar
		e.Router.AddRoute(echo.Route{
			Method: http.MethodGet,
			Path:   "/api/dishduty/calendar",
			Handler: func(c echo.Context) error {
				startDateParam := c.QueryParam("start_date")
				endDateParam := c.QueryParam("end_date")

				if startDateParam == "" || endDateParam == "" {
					return apis.NewBadRequestError("Bad Request: start_date and end_date query parameters are required.", nil)
				}

				// Validate date format (basic check)
				dateRegex := `^\d{4}-\d{2}-\d{2}$`
				startDateMatch, err := regexp.MatchString(dateRegex, startDateParam)
				if err != nil || !startDateMatch {
					return apis.NewBadRequestError("Bad Request: start_date must be in YYYY-MM-DD format.", nil)
				}

				endDateMatch, err := regexp.MatchString(dateRegex, endDateParam)
				if err != nil || !endDateMatch {
					return apis.NewBadRequestError("Bad Request: end_date must be in YYYY-MM-DD format.", nil)
				}

				// startDateFull := startDateParam + " 00:00:00.000Z" // Unused
				// endDateFull := endDateParam + " 23:59:59.999Z" // Ensure end of day // Unused

				calendarEvents := []map[string]interface{}{}

				// 1. Fetch actual assignments
				assignments, err := dao.FindRecordsByFilter(
					"assignments",
					"date >= {:start} AND date <= {:end} ORDER BY date",
					"id", 0, 100, // Required parameters for v0.19.4: sort field, offset, limit
					dbx.Params{"start": startDateParam, "end": endDateParam}, // Corrected to use YMD for date fields
				)

				if err != nil && err.Error() != "sql: no rows in result set" {
					log.Printf("Error finding assignments for calendar: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				// Process assignments
				for _, assignment := range assignments {
					workerId := assignment.GetString("worker_id")
					worker, err := dao.FindRecordById("workers", workerId)
					if err != nil {
						log.Printf("Error finding worker for assignment: %v", err)
						continue
					}

					dateStr := assignment.GetString("date")
					date, err := time.Parse(timeLayoutFull, dateStr)
					if err != nil {
						log.Printf("Error parsing assignment date: %v", err)
						continue
					}

					calendarEvents = append(calendarEvents, map[string]interface{}{
						"title": worker.GetString("name"),
						"start": formatDateToYMDGo(date),
						"extendedProps": map[string]interface{}{
							"worker_id": workerId,
							"status":    assignment.GetString("status"),
							"type":      "assignment",
						},
					})
				}

				// 2. Project queue items into the future
				// First, find the last actual assignment date
				lastAssignmentDate := getTodayYMDGo() // Default to today if no assignments
				if len(assignments) > 0 {
					// Find the latest assignment date
					var latestDate time.Time
					for _, assignment := range assignments {
						dateStr := assignment.GetString("date")
						date, err := time.Parse(timeLayoutFull, dateStr)
						if err != nil {
							continue
						}
						if latestDate.IsZero() || date.After(latestDate) {
							latestDate = date
						}
					}
					if !latestDate.IsZero() {
						lastAssignmentDate = formatDateToYMDGo(latestDate)
					}
				}

				// Get queue items
				queueItems, err := dao.FindRecordsByFilter(
					"assignment_queue",
					"1=1 ORDER BY {{order}} ASC", // Order by queue position
					"id", 0, 100,                 // Required parameters for v0.19.4: sort field, offset, limit
					dbx.Params{},
				)

				if err != nil && err.Error() != "sql: no rows in result set" {
					log.Printf("Error finding queue items for calendar: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				// Project queue items
				currentDate, err := addDaysToYMDGo(lastAssignmentDate, 1) // Start from day after last assignment
				if err != nil {
					log.Printf("Error calculating start date for queue projection: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				endDate, err := parseYMDToGoTime(endDateParam)
				if err != nil {
					log.Printf("Error parsing end date: %v", err)
					return apis.NewApiError(http.StatusInternalServerError, "Internal Server Error.", err)
				}

				// Process each queue item
				for _, queueItem := range queueItems {
					workerId := queueItem.GetString("worker_id")
					worker, err := dao.FindRecordById("workers", workerId)
					if err != nil {
						log.Printf("Error finding worker for queue item: %v", err)
						continue
					}

					durationDays := queueItem.GetInt("duration_days")

					// Add projected assignments for this queue item
					for i := 0; i < durationDays; i++ {
						projectedDate, err := addDaysToYMDGo(currentDate, i)
						if err != nil {
							log.Printf("Error calculating projected date: %v", err)
							continue
						}

						// Check if the projected date is within the requested range
						projectedDateTime, err := parseYMDToGoTime(projectedDate)
						if err != nil {
							log.Printf("Error parsing projected date: %v", err)
							continue
						}

						if projectedDateTime.After(endDate) {
							break // Stop if we've gone beyond the requested end date
						}

						calendarEvents = append(calendarEvents, map[string]interface{}{
							"title": worker.GetString("name") + " (queued)",
							"start": projectedDate,
							"extendedProps": map[string]interface{}{
								"worker_id":     workerId,
								"status":        "queued",
								"type":          "queue_projection",
								"queue_item_id": queueItem.Id,
								"queue_order":   queueItem.GetInt("order"),
							},
						})
					}

					// Move current date forward for the next queue item
					currentDate, err = addDaysToYMDGo(currentDate, durationDays)
					if err != nil {
						log.Printf("Error calculating next start date: %v", err)
						break
					}
				}

				return c.JSON(http.StatusOK, calendarEvents)
			},
		})

		return nil
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
