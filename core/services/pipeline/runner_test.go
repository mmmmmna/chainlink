package pipeline_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink/core/internal/cltest"
	"github.com/smartcontractkit/chainlink/core/services/job"
	"github.com/smartcontractkit/chainlink/core/services/pipeline"
)

func TestRunner(t *testing.T) {
	config, oldORM, cleanupDB := cltest.BootstrapThrowawayORM(t, "chainlink_test_temp_pipeline_runner", true)
	defer cleanupDB()
	db := oldORM.DB

	var httpURL string
	{
		mockElectionWinner, cleanupElectionWinner := cltest.NewHTTPMockServer(t, http.StatusOK, "POST", `Hal Finney`)
		defer cleanupElectionWinner()
		mockVoterTurnout, cleanupVoterTurnout := cltest.NewHTTPMockServer(t, http.StatusOK, "POST", `{"data": {"result": 62.57}}`)
		defer cleanupVoterTurnout()
		mockHTTP, cleanupHTTP := cltest.NewHTTPMockServer(t, http.StatusOK, "POST", `{"turnout": 61.942}`)
		defer cleanupHTTP()

		_, bridgeER := cltest.NewBridgeType(t, "election_winner", mockElectionWinner.URL)
		err := db.Create(bridgeER).Error
		require.NoError(t, err)

		_, bridgeVT := cltest.NewBridgeType(t, "voter_turnout", mockVoterTurnout.URL)
		err = db.Create(bridgeVT).Error
		require.NoError(t, err)

		httpURL = mockHTTP.URL
	}

	pipelineORM := pipeline.NewORM(db, config)
	runner := pipeline.NewRunner(pipelineORM, config)
	jobORM := job.NewORM(db, config, pipelineORM)
	defer jobORM.Close()

	// Need a job in order to create a run
	ocrSpec, dbSpec := makeOCRJobSpecWithHTTPURL(t, db, httpURL)
	err := jobORM.CreateJob(context.Background(), dbSpec, ocrSpec.TaskDAG())
	require.NoError(t, err)

	runner.Start()
	defer runner.Stop()

	runID, err := runner.CreateRun(context.Background(), dbSpec.ID, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = runner.AwaitRun(ctx, runID)
	require.NoError(t, err)

	// Verify the final pipeline results
	results, err := runner.ResultsForRun(context.Background(), runID)
	require.NoError(t, err)

	assert.Len(t, results, 2)
	assert.NoError(t, results[0].Error)
	assert.NoError(t, results[1].Error)
	assert.Equal(t, "6225.6", results[0].Value)
	assert.Equal(t, "Hal Finney", results[1].Value)

	// Verify individual task results
	var runs []pipeline.TaskRun
	err = db.
		Preload("PipelineTaskSpec").
		Where("pipeline_run_id = ?", runID).
		Find(&runs).Error
	assert.NoError(t, err)
	assert.Len(t, runs, 9)

	for _, run := range runs {
		if run.DotID() == "answer2" {
			assert.Equal(t, "Hal Finney", run.Output.Val)
		} else if run.DotID() == "ds2" {
			assert.Equal(t, `{"turnout": 61.942}`, run.Output.Val)
		} else if run.DotID() == "ds2_parse" {
			assert.Equal(t, float64(61.942), run.Output.Val)
		} else if run.DotID() == "ds2_multiply" {
			assert.Equal(t, "6194.2", run.Output.Val)
		} else if run.DotID() == "ds1" {
			assert.Equal(t, `{"data": {"result": 62.57}}`, run.Output.Val)
		} else if run.DotID() == "ds1_parse" {
			assert.Equal(t, float64(62.57), run.Output.Val)
		} else if run.DotID() == "ds1_multiply" {
			assert.Equal(t, "6257", run.Output.Val)
		} else if run.DotID() == "answer1" {
			assert.Equal(t, "6225.6", run.Output.Val)
		} else if run.DotID() == "__result__" {
			assert.Equal(t, []interface{}{"6225.6", "Hal Finney"}, run.Output.Val)
		} else {
			t.Fatalf("unknown task '%v'", run.DotID())
		}
	}
}
