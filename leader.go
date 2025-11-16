package omnipaxos

import (
	"math"

	"github.com/rs/zerolog/log"
)

type Role int

const (
	FOLLOWER Role = iota
	LEADER
)

type LeaderRequest struct {
	S   int
	Rnd BallotNumber
}

type PrepareRequest struct {
	Me     int
	N      BallotNumber
	AccRnd BallotNumber
	LogIdx int
	DecIdx int
}

// checkLeader must be called while holding op.mu
func (op *OmniPaxos) checkLeader() {
	max := BallotNumber{math.MinInt, math.MinInt}
	for _, ballot := range op.ballots {
		if !ballot.Qc {
			continue
		}
		if ballot.BallotNumber.Compare(max) > 0 {
			max = ballot.BallotNumber
		}
	}

	switch (max).Compare(op.L) {
	case -1:
		// start new election
		log.Info().Msgf("checkLeader(%v): inc(l), qc = true", op.R)
		op.B = BallotNumber{op.L.Value + 1, op.B.Pid}
		op.qc = true
	case 1:
		op.L = max
		go func() {
			op.serializeCh <- struct{}{}        // acquire semaphore
			defer func() { <-op.serializeCh }() // release
			op.leaderFromBLE(max.Pid, max)
		}()
	}
}

// method to initialize all states for Ballot Election Algorithm(Figure 3.2)
func (op *OmniPaxos) leaderFromBLE(s int, n BallotNumber) {
	op.mu.Lock()
	defer op.mu.Unlock()

	if s == op.me && n.Compare(op.promisedRnd) == 1 {
		// Reset all volatile state of leader (per paper)
		op.promises = make(map[int]Promise)
		op.maxProm = Promise{}
		op.accepted = make([]int, len(op.peers))
		op.buffer = make([]any, 0)

		// become da leader!
		op.role = LEADER
		op.phase = PREPARE
		op.currentRnd = n
		op.promisedRnd = n

		// TODO : decided suffix?
		next := Promise{op.acceptedRnd, len(op.log), op.me, op.decidedIdx, op.suffix(op.decidedIdx)}
		op.promises[op.me] = next

		log.Info().Msgf("Server %v became leader with ballot %v", op.me, n)

		// TODO: how to send?
		// send⟨Prepare, currentRnd, acceptedRnd, |log|, decidedIdx⟩ to all peers

		// wg := sync.WaitGroup{}
		// Capture values before spawning goroutines to avoid races
		round := op.R

		for i, peer := range op.peers {
			if i == op.me {
				continue
			}
			i, peer := i, peer
			// send⟨Prepare, currentRnd, acceptedRnd, |log|, decidedIdx⟩ to all peers
			prepare_request := PrepareRequest{op.me, op.currentRnd, op.acceptedRnd, len(op.log), op.decidedIdx}

			go func() {
				op.serializeCh <- struct{}{}        // acquire semaphore
				defer func() { <-op.serializeCh }() // release
				if !peer.Call("OmniPaxos.RecievePrepare", &prepare_request, &DummyReply{}) {
					log.Info().Msgf("checkLeader(%v): no response from %v", round, i)
					return
				}
			}()
		}

	} else {
		op.role = FOLLOWER
	}
}
