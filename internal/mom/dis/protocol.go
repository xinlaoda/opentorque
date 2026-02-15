package dis

// PBS Batch Request Types - must match pbs_batchreqtype_db.h
const (
	BatchConnect       = 0
	BatchQueueJob      = 1
	BatchJobCred       = 2
	BatchJobScript     = 3
	BatchRdytoCommit   = 4
	BatchCommit        = 5
	BatchDeleteJob     = 6
	BatchHoldJob       = 7
	BatchLocateJob     = 8
	BatchManager       = 9
	BatchMessJob       = 10
	BatchModifyJob     = 11
	BatchMoveJob       = 12
	BatchReleaseJob    = 13
	BatchRerun         = 14
	BatchRunJob        = 15
	BatchSelectJobs    = 16
	BatchShutdown      = 17
	BatchSignalJob     = 18
	BatchStatusJob     = 19
	BatchStatusQue     = 20
	BatchStatusSvr     = 21
	BatchTrackJob      = 22
	BatchAsyRunJob     = 23
	BatchRescq         = 24
	BatchReserveResc   = 25
	BatchReleaseResc   = 26
	BatchCheckpointJob = 27
	BatchAsyModifyJob  = 28
	BatchQueueJob2     = 29
	BatchCommit2       = 30
	BatchJobScript2    = 31
	BatchStageIn       = 48
	BatchAuthenUser    = 49
	BatchOrderJob      = 50
	BatchSelStat       = 51
	BatchRegistDep     = 52
	BatchReturnFiles   = 53
	BatchCopyFiles     = 54
	BatchDelFiles      = 55
	BatchJobObit       = 56
	BatchMvJobFile     = 57
	BatchStatusNode    = 58
	BatchDisconnect    = 59
	BatchAsySignalJob  = 60
	BatchAltAuthenUser = 61
	BatchGpuCtrl       = 62
)

// PBS Batch Reply Choice Types (must match libpbs.h BATCH_REPLY_CHOICE_*)
const (
	ReplyChoiceNull     = 1 // no reply data, just code
	ReplyChoiceQueue    = 2 // job ID (QueueJob reply)
	ReplyChoiceRdytoCom = 3 // job ID (ReadyToCommit reply)
	ReplyChoiceCommit   = 4 // job ID (Commit reply)
	ReplyChoiceSelect   = 5 // select list
	ReplyChoiceStatus   = 6 // status objects
	ReplyChoiceText     = 7 // text string
	ReplyChoiceLocate   = 8 // locate info
)

// PBS Error Codes (common ones)
const (
	PbsErrNone       = 0
	PbsErrNoAttr     = 15001
	PbsErrNoMem      = 15002
	PbsErrInternal   = 15003
	PbsErrSystem     = 15004
	PbsErrPerm       = 15007
	PbsErrUnkjobid   = 15001
	PbsErrBadHost    = 15019
	PbsErrJobExist   = 15014
	PbsErrNoConnect  = 15020
	PbsErrProtocol   = 15044
	PbsErrUnknownReq = 15048
)

// Inter-MOM protocol commands
const (
	IMAllOkay       = 0
	IMJoinJob       = 1
	IMKillJob       = 2
	IMSpawnTask     = 3
	IMGetTasks      = 4
	IMSignalTask    = 5
	IMObitTask      = 6
	IMPollJob       = 7
	IMGetInfo       = 8
	IMGetResc       = 9
	IMAbortJob      = 10
	IMGetTid        = 11
	IMRadixAllOk    = 12
	IMJoinJobRadix  = 13
	IMKillJobRadix  = 14
	IMFence         = 15
	IMConnect       = 16
	IMDisconnect    = 17
	IMMax           = 18
	IMError         = 99
)

// Job States
const (
	JobStateTransit = 0
	JobStateQueued  = 1
	JobStateHeld    = 2
	JobStateWaiting = 3
	JobStateRunning = 4
	JobStateExiting = 5
	JobStateComplete = 6
)

// Job Substates
const (
	JobSubstateTransit     = 0
	JobSubstateQueued      = 10
	JobSubstateHeld        = 20
	JobSubstateWaiting     = 30
	JobSubstatePrerun      = 41
	JobSubstateStarting    = 42
	JobSubstateRunning     = 42
	JobSubstateExiting     = 50
	JobSubstatePreObit     = 57
	JobSubstateObit        = 58
	JobSubstateComplete    = 59
	JobSubstateReturnFiles = 54
	JobSubstateExited      = 59
)

// BatchRequestName returns a human-readable name for a batch request type.
func BatchRequestName(reqType int) string {
	names := map[int]string{
		BatchConnect:       "Connect",
		BatchQueueJob:      "QueueJob",
		BatchJobCred:       "JobCred",
		BatchJobScript:     "JobScript",
		BatchRdytoCommit:   "ReadyToCommit",
		BatchCommit:        "Commit",
		BatchDeleteJob:     "DeleteJob",
		BatchHoldJob:       "HoldJob",
		BatchRunJob:        "RunJob",
		BatchShutdown:      "Shutdown",
		BatchSignalJob:     "SignalJob",
		BatchStatusJob:     "StatusJob",
		BatchReturnFiles:   "ReturnFiles",
		BatchCopyFiles:     "CopyFiles",
		BatchDelFiles:      "DeleteFiles",
		BatchJobObit:       "JobObit",
		BatchDisconnect:    "Disconnect",
		BatchAsySignalJob:  "AsyncSignalJob",
		BatchStatusNode:    "StatusNode",
		BatchModifyJob:     "ModifyJob",
		BatchCheckpointJob: "CheckpointJob",
	}
	if name, ok := names[reqType]; ok {
		return name
	}
	return "Unknown"
}
