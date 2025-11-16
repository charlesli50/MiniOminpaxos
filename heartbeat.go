package omnipaxos

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type HBRequest struct {
	Rnd int
}

type PrepareRecoveringFollowerRequest struct {
	Me int
}

type HBReply struct {
	Rnd    int
	Ballot Ballot
}
type DummyReply struct{}

func (op *OmniPaxos) sendHBRequest(wg *sync.WaitGroup, ballotsCh chan Ballot, timeoutCh <-chan time.Time) {
	op.mu.Lock()
	role := op.role
	phase := op.phase
	op.mu.Unlock()

	log.Info().Msgf("send HB Request (%v) [%v | %v] Heartbeat being made!", op.me, role, phase)

	// iterate over peers and send those requests
	for i, peer := range op.peers {
		if i == op.me {
			continue
		}
		wg.Add(1)
		i, peer := i, peer // capture loop variables
		go func() {
			defer wg.Done()
			reply := HBReply{}
			if !peer.Call("OmniPaxos.ReceiveHBRequest", &HBRequest{op.R}, &reply) {
				log.Info().Msgf("startTimer(%v): no heartbeat from %v", op.R, i)
				return
			}
			op.mu.Lock()
			defer op.mu.Unlock()

			if reply.Rnd == op.R {
				select {
				case ballotsCh <- reply.Ballot:
				case <-timeoutCh:
					return
				}
			}
		}()
	}
}
func (op *OmniPaxos) ReceiveHBRequest(args *HBRequest, res *HBReply) {
	op.mu.Lock()
	defer op.mu.Unlock()

	// past 3 rounds heart beat
	op.LinkLastHBRound = max(args.Rnd, op.LinkLastHBRound)

	// if need to recover
	if op.LinkDrop {
		op.LinkDrop = false
		op.role = FOLLOWER
		op.phase = RECOVER

		for i, peer := range op.peers {
			if i == op.me {
				continue
			}
			peer := peer
			go func() {
				op.serializeCh <- struct{}{}        // acquire semaphore
				defer func() { <-op.serializeCh }() // release
				if !peer.Call("OmniPaxos.PrepareRecoveringFollower", &PrepareRecoveringFollowerRequest{op.me}, &DummyReply{}) {
					log.Info().Msgf("no response from peer when attempting to recover")
					return
				}
			}()
		}
	}

	rnd := args.Rnd
	res.Rnd = rnd
	res.Ballot = Ballot{op.B, op.qc}
}

func (op *OmniPaxos) PrepareRecoveringFollower(args *PrepareRecoveringFollowerRequest, res *DummyReply) {
	op.mu.Lock()
	defer op.mu.Unlock()

	if op.role != LEADER {
		return
	}

	follower := op.peers[args.Me]
	currentRnd := op.currentRnd
	acceptedRnd := op.acceptedRnd
	logSize := len(op.log)
	decIdx := op.decidedIdx

	go func() {
		op.serializeCh <- struct{}{}        // acquire semaphore
		defer func() { <-op.serializeCh }() // release
		if !follower.Call("OmniPaxos.RecievePrepare", &PrepareRequest{op.me, currentRnd, acceptedRnd, logSize, decIdx}, &DummyReply{}) {
			log.Info().Msgf("recieve prepare failed")
		}
	}()

}

// called when reconnecting to single peer
func (op *OmniPaxos) reconnected(pid int) {
	if pid == op.me {
		return
	}
	op.Info("reconnected to %v: l: %v, decidedIdx: %v", pid, op.L, op.decidedIdx)

	// Send PrepareRecoveringFollower if reconnecting to leader
	if pid == op.L.Pid {
		op.role = FOLLOWER
		op.phase = RECOVER
		op.Info("reconnected to %v, state: %v %v", pid, op.role, op.phase)
		prepareRecoveryRequest := PrepareRecoveringFollowerRequest{op.me}
		peer := op.peers[pid]
		go func() {
			op.serializeCh <- struct{}{}        // acquire semaphore
			defer func() { <-op.serializeCh }() // release
			if !peer.Call("OmniPaxos.PrepareRecoveringFollower", &prepareRecoveryRequest, &DummyReply{}) {
				op.Info("PrepareRecoveringFollower failed to %v", pid)
			}
		}()
	}
}
