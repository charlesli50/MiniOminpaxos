package omnipaxos

import "github.com/rs/zerolog/log"

// work on figure (3.4) Promise from Follower f
// connected to leader.go

type PromiseFromFollowerRequest struct {
	Me     int
	N      BallotNumber
	AccRnd BallotNumber
	LogIdx int
	DecIdx int
	Sfx    []any
}

type AcceptSyncFromLeaderRequest struct {
	Me      int
	N       BallotNumber
	Sfx     []any
	Syncidx int
}

// Recieve Prepare Request
func (op *OmniPaxos) RecievePrepare(args *PrepareRequest, res *DummyReply) {
	op.mu.Lock()

	// Reciever Implementation(3.3)

	// return if promisedRnd > n
	if op.promisedRnd.Compare(args.N) == 1 {
		op.mu.Unlock()
		return
	}
	//  State ← (FOLLOWER, PREPARE)
	op.role = FOLLOWER
	op.phase = PREPARE

	// promsiedRnd  ←  n
	op.promisedRnd = args.N
	sfx := []any{}
	if op.acceptedRnd.Compare(args.AccRnd) == 1 {
		sfx = op.suffix(args.DecIdx)
	} else if op.acceptedRnd == args.AccRnd {
		sfx = op.suffix(args.LogIdx)
	}

	leader := op.peers[args.Me]
	accRnd := op.acceptedRnd
	logLen := len(op.log)
	decidedIdx := op.decidedIdx
	op.mu.Unlock()

	go func() {
		op.serializeCh <- struct{}{}        // acquire semaphore
		defer func() { <-op.serializeCh }() // release
		req := PromiseFromFollowerRequest{op.me, args.N, accRnd, logLen, decidedIdx, sfx}

		if !leader.Call("OmniPaxos.PromiseFromFollower", &req, &DummyReply{}) {
			log.Info().Msgf("ReceievePrepare(%v): no response from %v", op.R, args.Me)
			return
		}
	}()
}

// Leader gets this
func (op *OmniPaxos) PromiseFromFollower(args *PromiseFromFollowerRequest, res *DummyReply) {
	op.mu.Lock()
	defer op.mu.Unlock()

	if args.N != op.currentRnd {
		return
	}

	if op.role != LEADER {
		return
	}

	op.promises[args.Me] = Promise{args.AccRnd, args.LogIdx, args.Me, args.DecIdx, args.Sfx}

	if op.role == LEADER && op.phase == PREPARE {
		// P1. return if |promises| < majority
		// op.mu.Lock()
		// defer op.mu.Unlock()
		log.Info().Msgf("Proimise From Follower %v - |Promises| = %v", op.me, op.promises)
		if len(op.promises) <= len(op.peers)/2 {
			return
		}

		// P2. maxProm ← the value with highest accRnd in promises
		// (and highest logIdx if equal)
		var maxPromSet bool
		for _, promise := range op.promises {
			if !maxPromSet || promise.Compare(op.maxProm) < 0 {
				op.maxProm = promise
				maxPromSet = true
			}
		}

		// P3. if maxProm.accRnd ≠ acceptedRnd then
		// log ← prefix(decidedIdx)
		if op.maxProm.accRnd != op.acceptedRnd {
			op.log = op.prefix(op.decidedIdx)
		}

		// P4. append maxProm.sfx to the log
		op.log = append(op.log, op.maxProm.sfx...)

		// P5. if stopped()then clear buffer else append buffer to the log
		if op.stopped() {
			op.buffer = make([]any, 0)
		} else {
			op.log = append(op.log, op.buffer...)
		}

		// P6. acceptedRnd ← currentRnd,
		// accepted[self] ← |log|, state ← (LEADER, ACCEPT)
		op.acceptedRnd = op.currentRnd
		op.accepted[op.me] = len(op.log)

		op.role = LEADER
		op.phase = ACCEPT

		// P7.
		// foreach p in promises:
		// let syncIdx ← if p.accRnd = maxProm.accRnd then
		// p.logIdx else p.decIdx,
		// send ⟨AcceptSync, currentRnd, suffix(syncIdx),
		// syncIdx⟩ to p.f

		for _, p := range op.promises {
			var syncidx int
			if p.accRnd == op.maxProm.accRnd {
				syncidx = p.logIdx
			} else {
				syncidx = p.decIdx
			}
			sfx := op.suffix(syncidx)
			peer := op.peers[p.f]
			me := op.me
			currentRnd := op.currentRnd

			go func() {
				op.serializeCh <- struct{}{}        // acquire semaphore
				defer func() { <-op.serializeCh }() // release
				req := AcceptSyncFromLeaderRequest{me, currentRnd, sfx, syncidx}
				if !peer.Call("OmniPaxos.AcceptSyncFromLeader", &req, &DummyReply{}) {
					log.Info().Msgf("accept sync from leader failed %v %v",
						me, p.f)
				}
			}()
		}
	}

	if op.role == LEADER && op.phase == ACCEPT {
		var syncidx int
		if args.AccRnd == op.maxProm.accRnd {
			syncidx = op.maxProm.logIdx
		} else {
			syncidx = args.DecIdx
		}

		peer := op.peers[args.Me]
		sfx := op.suffix(syncidx)
		me := op.me
		currentRnd := op.currentRnd
		decidedIdx := op.decidedIdx

		go func() {
			op.serializeCh <- struct{}{}        // acquire semaphore
			defer func() { <-op.serializeCh }() // release
			req := AcceptSyncFromLeaderRequest{me, currentRnd, sfx, syncidx}
			if !peer.Call("OmniPaxos.AcceptSyncFromLeader", &req, &DummyReply{}) {
				log.Info().Msgf("accept sync from leader failed %v %v",
					me, args.Me)
			}
		}()

		if decidedIdx > args.DecIdx {
			go func() {
				op.serializeCh <- struct{}{}        // acquire semaphore
				defer func() { <-op.serializeCh }() // release
				if !peer.Call("OmniPaxos.DecideFromLeader", &DecideFromLeaderRequest{me, currentRnd, decidedIdx}, &DummyReply{}) {
					log.Info().Msgf("checkLeader(%v): no response from %v", op.R, args.Me)
					return
				}
			}()
		}

	}
}

func (op *OmniPaxos) AcceptSyncFromLeader(args *AcceptSyncFromLeaderRequest, res *DummyReply) {
	op.mu.Lock()

	if op.promisedRnd != args.N || op.role != FOLLOWER || (op.phase != PREPARE && op.phase != RECOVER) {
		op.mu.Unlock()
		return
	}

	op.acceptedRnd = args.N
	op.role = FOLLOWER
	op.phase = ACCEPT

	op.log = op.prefix(args.Syncidx)
	op.log = append(op.log, args.Sfx...)

	leader := op.peers[args.Me]
	logLen := len(op.log)
	op.mu.Unlock()

	go func() {
		op.serializeCh <- struct{}{}        // acquire semaphore
		defer func() { <-op.serializeCh }() // release
		if !leader.Call("OmniPaxos.AcceptedFromFollower", &AcceptedFromFollowerRequest{op.me, args.N, logLen}, &DummyReply{}) {
			log.Info().Msgf("fail to accept from leader %v => %v", op.R, args.Me)
			return
		}
	}()

}
