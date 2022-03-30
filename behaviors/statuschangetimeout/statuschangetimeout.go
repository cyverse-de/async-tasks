package statuschangetimeout

import (
	"context"
	"time"

	"github.com/cyverse-de/async-tasks/database"
	"github.com/cyverse-de/async-tasks/model"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type StatusChangeTimeoutData struct {
	StartStatus string `mapstructure:"start_status"`
	EndStatus   string `mapstructure:"end_status"`
	Timeout     string `mapstructure:"timeout"`
	Complete    bool   `mapstructure:"complete"`
	Delete      bool   `mapstructure:"delete"`
}

func rollbackLogError(tx *database.DBTx, log *logrus.Entry) {
	err := tx.Rollback()
	if err != nil {
		log.Error(err)
	}
}

func processSingleTask(ctx context.Context, log *logrus.Entry, db *database.DBConnection, ID string) error {
	select {
	// If the context is cancelled, don't bother
	case <-ctx.Done():
		return nil
	default:
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackLogError(tx, log)

	fullTask, err := tx.GetTask(ctx, ID, true)
	if err != nil {
		err = errors.Wrap(err, "failed getting task")
		log.Error(err)
		return err
	}

	var comparisonTimestamp time.Time
	var comparisonStatus string
	if len(fullTask.Statuses) == 0 {
		comparisonTimestamp = *fullTask.StartDate
	} else {
		for _, status := range fullTask.Statuses {
			if status.CreatedDate.After(comparisonTimestamp) {
				comparisonTimestamp = status.CreatedDate
				comparisonStatus = status.Status
			}
		}
	}

	log.Infof("Most recent timestamp for task %s: %s", ID, comparisonTimestamp)

	for _, behavior := range fullTask.Behaviors {
		// only one of each type because of the DB FK
		if behavior.BehaviorType == "statuschangetimeout" {
			data, ok := behavior.Data["statuses"].([]interface{})
			if !ok {
				err = errors.New("Behavior data is not an array")
				log.Error(err)
				return err
			}
			for _, datum := range data {
				var taskData StatusChangeTimeoutData
				err := mapstructure.Decode(datum, &taskData)
				if err != nil {
					// don't die here, let it try other behaviors
					log.Error(errors.Wrap(err, "failed decoding behavior"))
					continue
				}

				timeout, err := time.ParseDuration(taskData.Timeout)
				if err != nil {
					// don't die here, let it try other behaviors
					log.Error(errors.Wrap(err, "failed parsing timeout duration"))
					continue
				}

				if comparisonTimestamp.Add(timeout).Before(time.Now()) && comparisonStatus == taskData.StartStatus {
					newstatus := model.AsyncTaskStatus{Status: taskData.EndStatus}
					err = tx.InsertTaskStatus(ctx, newstatus, ID)
					if err != nil {
						// do die here, because the transaction is probably dead
						err = errors.Wrap(err, "failed inserting task status")
						log.Error(err)
						return err
					}
					if taskData.Complete {
						err = tx.CompleteTask(ctx, ID)
						if err != nil {
							// do die here, because the transaction is probably dead
							err = errors.Wrap(err, "failed setting task complete")
							log.Error(err)
							return err
						}
					}
					if taskData.Delete {
						err = tx.DeleteTask(ctx, ID)
						if err != nil {
							// do die here, because the transaction is probably dead
							err = errors.Wrap(err, "failed deleting task")
							log.Error(err)
							return err
						}
					}
					log.Infof("Updated task with time %s and timeout %s from '%s' to '%s', set complete: %t, deleted: %t", comparisonTimestamp, timeout, comparisonStatus, taskData.EndStatus, taskData.Complete, taskData.Delete)
				} else {
					log.Infof("Task was not ready to update given time %s, timeout %s, and status '%s'", comparisonTimestamp, timeout, comparisonStatus)
				}
			}
		}
	}

	err = tx.Commit()
	if err != nil {
		log.Error(errors.Wrap(err, "failed committing transaction"))
	}

	return nil
}

func Processor(ctx context.Context, log *logrus.Entry, _ time.Time, db *database.DBConnection) error {
	filter := database.TaskFilter{
		BehaviorTypes: []string{"statuschangetimeout"},
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackLogError(tx, log)

	tasks, err := tx.GetTasksByFilter(ctx, filter, "end_date IS NOT NULL DESC")
	if err != nil {
		return err
	}

	rollbackLogError(tx, log)

	log.Infof("Tasks with statuschangetimeout behavior: %d", len(tasks))

ProcessLoop:
	for _, task := range tasks {
		select {
		// If the context is cancelled, don't bother
		case <-ctx.Done():
			log.Info("Not continuing to process tasks due to a canceled context.")
			break ProcessLoop
		default:
		}

		err = processSingleTask(ctx, log, db, task.ID)
		if err != nil {
			log.Error(errors.Wrap(err, "failed processing a task"))
		}
	}

	return nil
}
