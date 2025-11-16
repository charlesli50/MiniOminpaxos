package omnipaxos

import (
	"github.com/rs/zerolog/log"
)

type AcceptFromLeaderRequest struct {
	Me     int
	N      BallotNumber
	C      any
	LogIdx int // Index where this entry should be placed
}

type AcceptedFromFollowerRequest struct {
	Me     int
	N      BallotNumber
	LogIdx int
}

type DecideFromLeaderRequest struct {
	Me     int
	N      BallotNumber
	DecIdx int
}

// Called by the tester to submit a log to your OmniPaxos server
// Implement this as described in Figure 3
func (op *OmniPaxos) Proposal(command interface{}) (int, int, bool) {
	op.serializeCh <- struct{}{}        // acquire semaphore
	defer func() { <-op.serializeCh }() // release
	log.Info().Msgf("Proposal being made!")
	index := -1
	ballot := -1
	isLeader := false

	// Your code here (A4).
	if op.stopped() {
		return index, ballot, isLeader
	}

	op.mu.Lock()
	role := op.role
	phase := op.phase
	op.mu.Unlock()

	if role == LEADER && phase == PREPARE {
		op.mu.Lock()
		defer op.mu.Unlock()
		op.buffer = append(op.buffer, command)
	}

	if role == LEADER && phase == ACCEPT {
		log.Info().Msgf("[%v | %v] Proposal being made!", role, phase)
		op.mu.Lock()
		op.log = append(op.log, command)
		op.accepted[op.me] = len(op.log)
		index = len(op.log) - 1
		// send Accept, curretnRnd, C to all promised followers
		rnd := op.R
		for _, promise := range op.promises {
			i := promise.f
			peer := op.peers[i]
			if i == op.me {
				continue
			}
			followerID := i
			currentRnd := op.currentRnd
			me := op.me
			cmd := command
			logIdx := index // Capture the index for this entry
			go func() {
				op.serializeCh <- struct{}{}        // acquire semaphore
				defer func() { <-op.serializeCh }() // release
				if !peer.Call("OmniPaxos.AcceptFromLeader", &AcceptFromLeaderRequest{me, currentRnd, cmd, logIdx}, &DummyReply{}) {
					log.Info().Msgf("fail to accept from leader %v => %v", rnd, followerID)
					return
				}
			}()
		}
		op.mu.Unlock()
	}

	if index != -1 {
		ballot = op.B.Value
		isLeader = true
	}

	return index, ballot, isLeader
}

// Follower 3.7
func (op *OmniPaxos) AcceptFromLeader(args *AcceptFromLeaderRequest, res *DummyReply) {
	op.serializeCh <- struct{}{}        // acquire semaphore
	defer func() { <-op.serializeCh }() // release
	log.Info().Msgf("Accept from leader!")
	op.mu.Lock()
	if op.promisedRnd != args.N || (op.role != FOLLOWER || op.phase != ACCEPT) {
		op.mu.Unlock()
		return
	}

	// Ensure log has enough capacity for the entry at LogIdx
	// Fill with empty entries if needed
	for len(op.log) <= args.LogIdx {
		op.log = append(op.log, nil)
	}
	op.log[args.LogIdx] = args.C

	logLen := len(op.log)
	leader := op.peers[args.Me]
	rnd := op.R
	op.mu.Unlock()

	go func() {
		op.serializeCh <- struct{}{}        // acquire semaphore
		defer func() { <-op.serializeCh }() // release
		if !leader.Call("OmniPaxos.AcceptedFromFollower", &AcceptedFromFollowerRequest{op.me, args.N, logLen}, &DummyReply{}) {
			log.Info().Msgf("fail to accept from leader %v => %v", rnd, args.Me)
			return
		}
	}()
}

// Leader 3.8
func (op *OmniPaxos) AcceptedFromFollower(args *AcceptedFromFollowerRequest, res *DummyReply) {
	log.Info().Msgf("accepted from follower!")
	op.mu.Lock()
	if op.currentRnd != args.N || (op.role != LEADER && op.phase != ACCEPT) {
		op.mu.Unlock()
		return
	}
	op.accepted[args.Me] = args.LogIdx

	count := 0
	for _, logIdx := range op.accepted {
		if logIdx >= args.LogIdx {
			count += 1
		}
	}

	if args.LogIdx > op.decidedIdx && count > len(op.peers)/2 {
		op.decidedIdx = args.LogIdx
		peersToNotify := make([]int, 0)
		for i := range op.peers {
			if i != op.me {
				peersToNotify = append(peersToNotify, i)
			}
		}
		decidedIdx := op.decidedIdx
		currentRnd := op.currentRnd
		rnd := op.R
		op.mu.Unlock()

		for _, i := range peersToNotify {
			peer := op.peers[i]
			go func(peerID int) {
				op.serializeCh <- struct{}{}        // acquire semaphore
				defer func() { <-op.serializeCh }() // release
				if !peer.Call("OmniPaxos.DecideFromLeader", &DecideFromLeaderRequest{op.me, currentRnd, decidedIdx}, &DummyReply{}) {
					log.Info().Msgf("checkLeader(%v): no response from %v", rnd, peerID)
					return
				}
			}(i)
		}
	} else {
		op.mu.Unlock()
	}
}

func (op *OmniPaxos) DecideFromLeader(args *DecideFromLeaderRequest, res *DummyReply) {
	op.serializeCh <- struct{}{}        // acquire semaphore
	defer func() { <-op.serializeCh }() // release
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.promisedRnd == args.N && op.role == FOLLOWER && op.phase == ACCEPT {
		op.decidedIdx = args.DecIdx
	}
}
