package dis

// PBS Batch Protocol Types
const (
	PbsBatchProtType = 2 // Standard batch protocol
	PbsBatchProtVer  = 2 // Protocol version
	ISProtocolType   = 4 // IS (Inter-Server) protocol type
)

// PBS Batch Request Types - must match pbs_batchreqtype_db.h
const (
	BatchReqConnect       = 0
	BatchReqQueueJob      = 1
	BatchReqJobCred       = 2
	BatchReqJobScript     = 3
	BatchReqRdytoCommit   = 4
	BatchReqCommit        = 5
	BatchReqDeleteJob     = 6
	BatchReqHoldJob       = 7
	BatchReqLocateJob     = 8
	BatchReqManager       = 9
	BatchReqMessJob       = 10
	BatchReqModifyJob     = 11
	BatchReqMoveJob       = 12
	BatchReqReleaseJob    = 13
	BatchReqRerun         = 14
	BatchReqRunJob        = 15
	BatchReqSelectJobs    = 16
	BatchReqShutdown      = 17
	BatchReqSignalJob     = 18
	BatchReqStatusJob     = 19
	BatchReqStatusQueue   = 20
	BatchReqStatusServer  = 21
	BatchReqTrackJob      = 22
	BatchReqAsyRunJob     = 23
	BatchReqRescq         = 24
	BatchReqReserveResc   = 25
	BatchReqReleaseResc   = 26
	BatchReqCheckpointJob = 27
	BatchReqAsyModifyJob  = 28
	BatchReqQueueJob2     = 29
	BatchReqCommit2       = 30
	BatchReqJobScript2    = 31
	BatchReqStageIn       = 48
	BatchReqAuthenUser    = 49
	BatchReqOrderJob      = 50
	BatchReqSelStat       = 51
	BatchReqRegistDep     = 52
	BatchReqReturnFiles   = 53
	BatchReqCopyFiles     = 54
	BatchReqDelFiles      = 55
	BatchReqJobObit       = 56
	BatchReqMvJobFile     = 57
	BatchReqStatusNode    = 58
	BatchReqDisconnect    = 59
	BatchReqAsySignalJob  = 60
	BatchReqAltAuthenUser = 61
	BatchReqGpuCtrl       = 62
	BatchReqAuthToken     = 63 // Token-based auth (replaces trqauthd for cross-platform)
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
	PbseUnkjobid     = 15001
	PbseNoAttr       = 15002
	PbseAttrRo       = 15003
	PbseBadReq       = 15004
	PbseQueNoDeflt   = 15005
	PbseUnkQue       = 15006
	PbsePerm         = 15007
	PbseJobExist     = 15014
	PbseBadHost      = 15019
	PbseNoConnect    = 15020
	PbseBadCred      = 15021
	PbseProtocol     = 15033
	PbseUnkNode      = 15039
	PbseInternal     = 15044
	PbseSystem       = 15045
	PbseUnkReq       = 15048
)

// Manager command types (qmgr)
const (
	MgrCmdNone   = 0
	MgrCmdCreate = 1
	MgrCmdDelete = 2
	MgrCmdSet    = 3
	MgrCmdUnset  = 4
	MgrCmdList   = 5
	MgrCmdPrint  = 6
	MgrCmdActive = 7
)

// Manager object types
const (
	MgrObjNone   = 0
	MgrObjServer = 1
	MgrObjQueue  = 2
	MgrObjJob    = 3
	MgrObjNode   = 4
)

// IS (Inter-Server) protocol commands
const (
	ISNull      = 0
	ISUpdate    = 1
	ISStatus    = 4
	ISGpuStatus = 6
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
		BatchReqConnect:       "Connect",
		BatchReqQueueJob:      "QueueJob",
		BatchReqJobCred:       "JobCred",
		BatchReqJobScript:     "JobScript",
		BatchReqRdytoCommit:   "ReadyToCommit",
		BatchReqCommit:        "Commit",
		BatchReqDeleteJob:     "DeleteJob",
		BatchReqHoldJob:       "HoldJob",
		BatchReqLocateJob:     "LocateJob",
		BatchReqManager:       "Manager",
		BatchReqModifyJob:     "ModifyJob",
		BatchReqMoveJob:       "MoveJob",
		BatchReqReleaseJob:    "ReleaseJob",
		BatchReqRunJob:        "RunJob",
		BatchReqSelectJobs:    "SelectJobs",
		BatchReqShutdown:      "Shutdown",
		BatchReqSignalJob:     "SignalJob",
		BatchReqStatusJob:     "StatusJob",
		BatchReqStatusQueue:   "StatusQueue",
		BatchReqStatusServer:  "StatusServer",
		BatchReqStatusNode:    "StatusNode",
		BatchReqAuthenUser:    "AuthenUser",
		BatchReqReturnFiles:   "ReturnFiles",
		BatchReqCopyFiles:     "CopyFiles",
		BatchReqDelFiles:      "DeleteFiles",
		BatchReqJobObit:       "JobObit",
		BatchReqDisconnect:    "Disconnect",
		BatchReqAsySignalJob:  "AsyncSignalJob",
		BatchReqCheckpointJob: "CheckpointJob",
		BatchReqAuthToken:     "AuthToken",
	}
	if name, ok := names[reqType]; ok {
		return name
	}
	return "Unknown"
}
