// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.etcd.io/etcd/pkg/v3/expect"
	"go.etcd.io/etcd/tests/v3/framework/e2e"
)

func TestCtlV3Lock(t *testing.T) {
	testCtl(t, testLock)
}

func TestCtlV3LockWithCmd(t *testing.T) {
	testCtl(t, testLockWithCmd)
}

func testLock(cx ctlCtx) {
	name := "a"

	holder, ch, err := ctlV3Lock(cx, name)
	require.NoError(cx.t, err)

	l1 := ""
	select {
	case <-time.After(2 * time.Second):
		cx.t.Fatalf("timed out locking")
	case l1 = <-ch:
		if !strings.HasPrefix(l1, name) {
			cx.t.Errorf("got %q, expected %q prefix", l1, name)
		}
	}

	// blocked process that won't acquire the lock
	blocked, ch, err := ctlV3Lock(cx, name)
	require.NoError(cx.t, err)
	select {
	case <-time.After(100 * time.Millisecond):
	case <-ch:
		cx.t.Fatalf("should block")
	}

	// overlap with a blocker that will acquire the lock
	blockAcquire, ch, err := ctlV3Lock(cx, name)
	require.NoError(cx.t, err)
	defer func(blockAcquire *expect.ExpectProcess) {
		err = blockAcquire.Stop()
		require.NoError(cx.t, err)
		blockAcquire.Wait()
	}(blockAcquire)

	select {
	case <-time.After(100 * time.Millisecond):
	case <-ch:
		cx.t.Fatalf("should block")
	}

	// kill blocked process with clean shutdown
	require.NoError(cx.t, blocked.Signal(os.Interrupt))
	err = e2e.CloseWithTimeout(blocked, time.Second)
	if err != nil {
		// due to being blocked, this can potentially get killed and thus exit non-zero sometimes
		require.ErrorContains(cx.t, err, "unexpected exit code")
	}

	// kill the holder with clean shutdown
	require.NoError(cx.t, holder.Signal(os.Interrupt))
	require.NoError(cx.t, e2e.CloseWithTimeout(holder, 200*time.Millisecond+time.Second))

	// blockAcquire should acquire the lock
	select {
	case <-time.After(time.Second):
		cx.t.Fatalf("timed out from waiting to holding")
	case l2 := <-ch:
		if l1 == l2 || !strings.HasPrefix(l2, name) {
			cx.t.Fatalf("expected different lock name, got l1=%q, l2=%q", l1, l2)
		}
	}
}

func testLockWithCmd(cx ctlCtx) {
	// exec command with zero exit code
	echoCmd := []string{"echo"}
	require.NoError(cx.t, ctlV3LockWithCmd(cx, echoCmd, expect.ExpectedResponse{Value: ""}))

	// exec command with non-zero exit code
	code := 3
	awkCmd := []string{"awk", fmt.Sprintf("BEGIN{exit %d}", code)}
	expect := expect.ExpectedResponse{Value: fmt.Sprintf("Error: exit status %d", code)}
	require.ErrorContains(cx.t, ctlV3LockWithCmd(cx, awkCmd, expect), expect.Value)
}

// ctlV3Lock creates a lock process with a channel listening for when it acquires the lock.
func ctlV3Lock(cx ctlCtx, name string) (*expect.ExpectProcess, <-chan string, error) {
	cmdArgs := append(cx.PrefixArgs(), "lock", name)
	proc, err := e2e.SpawnCmd(cmdArgs, cx.envMap)
	outc := make(chan string, 1)
	if err != nil {
		close(outc)
		return proc, outc, err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		s, xerr := proc.ExpectFunc(ctx, func(string) bool { return true })
		if xerr != nil {
			require.ErrorContains(cx.t, xerr, "Error: context canceled")
		}
		outc <- s
	}()
	return proc, outc, err
}

// ctlV3LockWithCmd creates a lock process to exec command.
func ctlV3LockWithCmd(cx ctlCtx, execCmd []string, as ...expect.ExpectedResponse) error {
	// use command as lock name
	cmdArgs := append(cx.PrefixArgs(), "lock", execCmd[0])
	cmdArgs = append(cmdArgs, execCmd...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e2e.SpawnWithExpectsContext(ctx, cmdArgs, cx.envMap, as...)
}
