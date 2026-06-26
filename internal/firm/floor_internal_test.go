package firm

import (
	"testing"

	"github.com/hnic/trading-floor/internal/wire"
)

func TestFloorRoutesPredictionMarketTasksToSeparateQueue(t *testing.T) {
	t.Setenv("FLOOR_WORKERS", "2")
	t.Setenv("FLOOR_PREDICTION_WORKERS", "1")
	t.Setenv("FLOOR_TASK_QUEUE_SIZE", "3")
	t.Setenv("FLOOR_PREDICTION_TASK_QUEUE_SIZE", "5")
	t.Setenv("FLOOR_TASK_OVERFLOW_SIZE", "7")
	t.Setenv("FLOOR_PREDICTION_TASK_OVERFLOW_SIZE", "11")

	floor := NewFloor(wire.NewManager(), "test-session")

	predictionQueue, predictionOverflow := floor.taskQueues(deskTask{desk: &Desk{Domain: "prediction_market"}})
	if predictionQueue != floor.predictionTaskQueue {
		t.Fatal("prediction-market desk was not routed to prediction task queue")
	}
	if predictionOverflow != floor.predictionOverflowQueue {
		t.Fatal("prediction-market desk was not routed to prediction overflow queue")
	}

	defaultQueue, defaultOverflow := floor.taskQueues(deskTask{desk: &Desk{Domain: "corporate"}})
	if defaultQueue != floor.taskQueue {
		t.Fatal("corporate desk was not routed to default task queue")
	}
	if defaultOverflow != floor.overflowQueue {
		t.Fatal("corporate desk was not routed to default overflow queue")
	}

	stats := floor.Stats()
	if stats.DefaultTaskQueueCap != 3 || stats.PredictionTaskQueueCap != 5 {
		t.Fatalf("unexpected queue capacities: %+v", stats)
	}
	if stats.DefaultOverflowCap != 7 || stats.PredictionOverflowCap != 11 {
		t.Fatalf("unexpected overflow capacities: %+v", stats)
	}
}

func TestFloorTaskTimeoutClampsToDeskStageBudget(t *testing.T) {
	t.Setenv("FLOOR_TASK_TIMEOUT", "5s")

	floor := NewFloor(wire.NewManager(), "test-session")

	if got, want := floor.taskTimeout, minimumFloorTaskTimeout(); got != want {
		t.Fatalf("expected task timeout clamp to %s, got %s", want, got)
	}
}
