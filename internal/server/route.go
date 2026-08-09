// Package server queue routing and admission.
//
// This file implements TODO 3.1 (automatic queue routing, queue_type=Route)
// and TODO 3.7 (target-queue admission gate). Both live in package-level
// functions so they can be unit-tested without a full Server: they only need a
// queue.Manager and a job.Manager.
package server

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/xinlaoda/opentorque/internal/job"
	"github.com/xinlaoda/opentorque/internal/queue"
)

// routeJob routes a job submitted to a route queue to the first destination
// (from RouteDestin, in order) whose admission gate passes. It returns the
// chosen destination, whether routing occurred, and an error when no
// destination accepts the job (in which case the caller must reject it).
func routeJob(qm *queue.Manager, jm *job.Manager, rj *job.Job, user string) (string, bool, error) {
	q := qm.GetQueue(rj.Queue)
	if q == nil || q.Type != queue.TypeRoute {
		return rj.Queue, false, nil
	}
	q.Mu.RLock()
	dests := append([]string(nil), q.RouteDestin...)
	q.Mu.RUnlock()
	if len(dests) == 0 {
		return "", false, fmt.Errorf("route queue %s has no route_destinations", rj.Queue)
	}
	for _, dest := range dests {
		dq := qm.GetQueue(dest)
		if dq == nil {
			log.Printf("[ROUTE] queue %s -> %s: unknown destination queue", rj.Queue, dest)
			continue
		}
		if err := admitToQueue(jm, dq, rj, user, true); err != nil {
			log.Printf("[ROUTE] queue %s -> %s rejected: %v", rj.Queue, dest, err)
			continue
		}
		log.Printf("[ROUTE] routing job %s from %s to %s", rj.ID, rj.Queue, dest)
		return dest, true, nil
	}
	return "", false, fmt.Errorf("no route destination accepted job %s", rj.ID)
}

// admitToQueue is the ordered admission gate (svr_chkque). It returns nil when
// a job may enter q, else an error describing the blocking condition. fromRoute
// is true when the job arrived via routing (so a from_route_only destination
// accepts it); direct qsub passes false so from_route_only rejects it.
func admitToQueue(jm *job.Manager, q *queue.Queue, rj *job.Job, user string, fromRoute bool) error {
	q.Mu.RLock()
	enabled := q.Enabled
	started := q.Started
	fromRouteOnly := qAttrTrue(q.Attrs["from_route_only"])
	maxJobs := q.MaxJobs
	maxRun := q.MaxRun
	maxUserJobs := q.MaxUserJobs
	maxUserRun := q.MaxUserRun
	aclUserEn := q.ACLUserEnabled
	aclUsers := append([]string(nil), q.ACLUsers...)
	aclGroupEn := q.ACLGroupEnabled
	aclGroups := append([]string(nil), q.ACLGroups...)
	aclHostEn := q.ACLHostEnabled
	aclHosts := append([]string(nil), q.ACLHosts...)
	disallowed := append([]string(nil), q.DisallowedTypes...)
	resMax := q.ResourceMax
	resMin := q.ResourceMin
	q.Mu.RUnlock()

	if !enabled {
		return fmt.Errorf("queue %s is disabled", q.Name)
	}
	if !started {
		return fmt.Errorf("queue %s is not started", q.Name)
	}
	if fromRouteOnly && !fromRoute {
		return fmt.Errorf("queue %s only accepts routed jobs", q.Name)
	}
	for _, jt := range jobTypes(rj) {
		for _, dt := range disallowed {
			if jt == dt {
				return fmt.Errorf("queue %s disallows job type %s", q.Name, dt)
			}
		}
	}
	if maxJobs > 0 {
		cur := jm.CountByStateInQueue(q.Name, job.StateQueued, job.StateProvisioning) +
			jm.CountByStateInQueue(q.Name, job.StateHeld) +
			jm.CountByStateInQueue(q.Name, job.StateWaiting)
		if cur >= maxJobs {
			return fmt.Errorf("queue %s is at max_queuable (%d)", q.Name, maxJobs)
		}
	}
	if maxRun > 0 && jm.CountByStateInQueue(q.Name, job.StateRunning) >= maxRun {
		return fmt.Errorf("queue %s is at max_running (%d)", q.Name, maxRun)
	}
	if maxUserJobs > 0 {
		if jm.CountQueuedByOwnerInQueue(q.Name, user) >= maxUserJobs {
			return fmt.Errorf("queue %s: user %s at max_user_queuable (%d)", q.Name, user, maxUserJobs)
		}
	}
	if maxUserRun > 0 {
		if jm.CountRunningByOwnerInQueue(q.Name, user) >= maxUserRun {
			return fmt.Errorf("queue %s: user %s at max_user_run (%d)", q.Name, user, maxUserRun)
		}
	}
	if aclUserEn && len(aclUsers) > 0 && !aclContains(strings.Join(aclUsers, ","), extractUser(user)) {
		return fmt.Errorf("queue %s: user %s not in acl_users", q.Name, user)
	}
	if aclGroupEn && len(aclGroups) > 0 && !aclContains(strings.Join(aclGroups, ","), rj.EGroup) {
		return fmt.Errorf("queue %s: group %s not in acl_groups", q.Name, rj.EGroup)
	}
	if aclHostEn && len(aclHosts) > 0 {
		// Resolve the submitting client host the same way queueAllowsSubmitHost
		// does (PBS_O_HOST, falling back to the host part of Job_Owner). Using
		// bare hostOf(user) breaks the allow-list for direct qsub because the
		// client sends Job_Owner without a \"@host\" suffix (1.6).
		host := submitHostOf(rj)
		if host == "" || !aclContains(strings.Join(aclHosts, ","), host) {
			return fmt.Errorf("queue %s: host %s not in acl_hosts", q.Name, host)
		}
	}
	return checkResourceInterval(resMin, resMax, rj)
}

// checkResourceInterval verifies the job's requested ncpus/walltime/mem fall
// within the queue's [resources_min, resources_max] interval. This is the key
// routing criterion for short-vs-long / small-vs-big destination selection.
func checkResourceInterval(resMin, resMax map[string]string, rj *job.Job) error {
	for _, name := range []string{"ncpus", "walltime", "mem"} {
		req := rj.ResourceReq[name]
		if req == "" {
			continue
		}
		var reqVal int64
		switch name {
		case "ncpus":
			reqVal, _ = strconv.ParseInt(req, 10, 64)
		case "walltime":
			reqVal = parseWalltimeSec(req)
		case "mem":
			reqVal = parseMemKB(req)
		}
		if minStr := resMin[name]; minStr != "" {
			if v := rescValue(name, minStr); v > reqVal {
				return fmt.Errorf("job %s below resources_min %s (%s)", rj.ID, name, minStr)
			}
		}
		if maxStr := resMax[name]; maxStr != "" {
			if v := rescValue(name, maxStr); v >= 0 && v < reqVal {
				return fmt.Errorf("job %s exceeds resources_max %s (%s)", rj.ID, name, maxStr)
			}
		}
	}
	return nil
}

// rescValue converts a queue resource-limit value to the same scale as the job
// request. walltime is seconds, mem is KB, ncpus is an integer. Unknown/0
// limits are treated as unlimited (-1 so they never reject).
func rescValue(name, v string) int64 {
	switch name {
	case "ncpus":
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case "walltime":
		return parseWalltimeSec(v)
	case "mem":
		return parseMemKB(v)
	}
	return -1
}

// hostOf returns the host portion of a "user@host" string (empty if none).
func hostOf(s string) string {
	if idx := strings.LastIndex(s, "@"); idx >= 0 {
		return s[idx+1:]
	}
	return ""
}

// qAttrTrue reports whether a generic queue attribute value is truthy.
func qAttrTrue(v string) bool {
	lower := strings.ToLower(strings.TrimSpace(v))
	return lower == "true" || lower == "1" || lower == "yes" || lower == "y"
}

// parseWalltimeSec parses "HH:MM:SS" (or "MM:SS" / plain seconds) into seconds.
func parseWalltimeSec(s string) int64 {
	parts := strings.Split(s, ":")
	if len(parts) == 3 {
		h, _ := strconv.ParseInt(parts[0], 10, 64)
		m, _ := strconv.ParseInt(parts[1], 10, 64)
		sec, _ := strconv.ParseInt(parts[2], 10, 64)
		return h*3600 + m*60 + sec
	}
	if len(parts) == 2 {
		m, _ := strconv.ParseInt(parts[0], 10, 64)
		sec, _ := strconv.ParseInt(parts[1], 10, 64)
		return m*60 + sec
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// parseMemKB parses a memory size ("512mb", "2gb", "1024") into KB.
func parseMemKB(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	var mult int64 = 1
	switch {
	case strings.HasSuffix(s, "kb"):
		s = s[:len(s)-2]
	case strings.HasSuffix(s, "mb"):
		s = s[:len(s)-2]
		mult = 1024
	case strings.HasSuffix(s, "gb"):
		s = s[:len(s)-2]
		mult = 1024 * 1024
	case strings.HasSuffix(s, "tb"):
		s = s[:len(s)-2]
		mult = 1024 * 1024 * 1024
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v * mult
}

// finalizeRoute routes a job to its destination and enforces the admission
// gate. For a route queue it picks the first accepting destination (the gate
// already passed during selection); for a direct submission it validates the
// target execution queue. Returns an error that rejects the job otherwise.
func (s *Server) finalizeRoute(rj *job.Job, owner string) error {
	dest, routed, err := routeJob(s.queueMgr, s.jobMgr, rj, owner)
	if err != nil {
		return err
	}
	rj.Mu.Lock()
	rj.Queue = dest
	rj.Mu.Unlock()
	if routed {
		return nil // admission already checked while selecting the destination
	}
	q := s.queueMgr.GetQueue(dest)
	if q == nil {
		return fmt.Errorf("queue %s not found", dest)
	}
	return admitToQueue(s.jobMgr, q, rj, owner, false)
}


// jobTypes returns the PBS job-type tags a job carries. Every job is "batch";
// it is additionally "interactive" (-I), "rerunable" (-r y), or "job_array"
// (-t) as applicable. Used by the queue disallowed_types gate (TODO 3.5).
func jobTypes(j *job.Job) []string {
	types := []string{"batch"}
	if j.Interactive {
		types = append(types, "interactive")
	}
	if strings.EqualFold(strings.TrimSpace(j.Rerunnable), "y") {
		types = append(types, "rerunable")
	}
	if j.JobArrayReq != "" {
		types = append(types, "job_array")
	}
	return types
}
