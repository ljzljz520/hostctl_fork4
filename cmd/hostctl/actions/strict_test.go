package actions

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guumaster/hostctl/pkg/types"
)

const conflictingHosts = `
127.0.0.1 localhost

# profile.on profile1
127.0.0.1 first.loc
# end

# profile.off profile2
# 10.0.0.2 first.loc
# end
`

const sameIPHosts = `
127.0.0.1 localhost

# profile.on profile1
127.0.0.1 first.loc
# end

# profile.off profile2
# 127.0.0.1 first.loc
# end
`

const dualStackHosts = `
127.0.0.1 localhost

# profile.on profile1
127.0.0.1 first.loc
# end

# profile.off profile2
# ::1 first.loc
# end
`

const agreedOnHosts = `
127.0.0.1 localhost

# profile.on profile1
127.0.0.1 first.loc
# end

# profile.on profile2
127.0.0.1 first.loc
# end
`

const threeProfilesHosts = `
127.0.0.1 localhost

# profile.on profile1
127.0.0.1 first.loc
# end

# profile.off profile2
# 10.0.0.2 first.loc
# end

# profile.off profile3
# 127.0.0.1 first.loc
# end
`

func writeHostsFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hosts")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	return path
}

func execHostctl(t *testing.T, path, stdin string, args ...string) (string, error) {
	t.Helper()

	cmd := NewRootCmd()

	out := bytes.NewBufferString("")
	cmd.SetOut(out)
	cmd.SetErr(out)

	if stdin != "" {
		cmd.SetIn(strings.NewReader(stdin))
	}

	cmd.SetArgs(append(args, "--host-file", path))

	err := cmd.Execute()

	return out.String(), err
}

func Test_StrictEnable(t *testing.T) {
	t.Run("conflicting enable fails with details and leaves hosts untouched", func(t *testing.T) {
		path := writeHostsFile(t, conflictingHosts)
		before, err := os.ReadFile(path)
		require.NoError(t, err)

		out, err := execHostctl(t, path, "", "enable", "profile2", "--strict")
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		msg := err.Error()
		assert.Contains(t, msg, "first.loc")
		assert.Contains(t, msg, "127.0.0.1")
		assert.Contains(t, msg, "10.0.0.2")
		assert.Contains(t, msg, "profile1")
		assert.Contains(t, msg, "profile2")
		assert.Contains(t, msg, "hosts file not modified")
		assert.Contains(t, out, "Using hosts file")

		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})

	t.Run("lenient default still allows the conflict", func(t *testing.T) {
		path := writeHostsFile(t, conflictingHosts)

		_, err := execHostctl(t, path, "", "enable", "profile2")
		require.NoError(t, err)

		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(after), "# profile.on profile2")
	})

	t.Run("same IP duplicates can be enabled", func(t *testing.T) {
		path := writeHostsFile(t, sameIPHosts)

		_, err := execHostctl(t, path, "", "enable", "profile2", "--strict")
		require.NoError(t, err)

		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(after), "# profile.on profile2")
	})

	t.Run("IPv4/IPv6 pair can be enabled", func(t *testing.T) {
		path := writeHostsFile(t, dualStackHosts)

		_, err := execHostctl(t, path, "", "enable", "profile2", "--strict")
		require.NoError(t, err)
	})

	t.Run("enable --all with conflicting disabled profile fails untouched", func(t *testing.T) {
		path := writeHostsFile(t, conflictingHosts)
		before, err := os.ReadFile(path)
		require.NoError(t, err)

		_, err = execHostctl(t, path, "", "enable", "--all", "--strict")
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})

	t.Run("disable always succeeds", func(t *testing.T) {
		path := writeHostsFile(t, conflictingHosts)

		_, err := execHostctl(t, path, "", "disable", "profile1", "--strict")
		require.NoError(t, err)
	})
}

func Test_StrictToggleBatch(t *testing.T) {
	// toggling profile2 (10.0.0.2) and profile3 (127.0.0.1) on together with
	// the already enabled profile1 produces an IPv4 disagreement
	path := writeHostsFile(t, threeProfilesHosts)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	_, err = execHostctl(t, path, "", "toggle", "profile2", "profile3", "--strict")
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ErrStrictConflict))
	assert.Contains(t, err.Error(), "profile2")
	assert.Contains(t, err.Error(), "profile3")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "batch toggle must be atomic")
}

func Test_StrictEnableOnly(t *testing.T) {
	path := writeHostsFile(t, threeProfilesHosts)

	// only profile2 active: a single profile mapping cannot clash
	_, err := execHostctl(t, path, "", "enable", "profile2", "--only", "--strict")
	require.NoError(t, err)

	afterFirst, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(afterFirst), "# profile.on profile2")
	assert.Contains(t, string(afterFirst), "# profile.off profile1")
	assert.Contains(t, string(afterFirst), "# profile.off profile3")

	// profile2 (10.0.0.2) + profile3 (127.0.0.1) together would conflict
	_, err = execHostctl(t, path, "", "enable", "profile2", "profile3", "--only", "--strict")
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ErrStrictConflict))

	afterSecond, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(afterFirst), string(afterSecond), "failed --only must not change the previous state")
}

func Test_StrictReplace(t *testing.T) {
	path := writeHostsFile(t, agreedOnHosts)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	_, err = execHostctl(t, path, "10.0.0.2 first.loc\n", "replace", "profile1", "--strict")
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ErrStrictConflict))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func Test_StrictAdd(t *testing.T) {
	t.Run("add new conflicting profile is rejected", func(t *testing.T) {
		path := writeHostsFile(t, sameIPHosts)
		before, err := os.ReadFile(path)
		require.NoError(t, err)

		_, err = execHostctl(t, path, "10.0.0.2 first.loc\n", "add", "profile3", "--strict")
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})

	t.Run("add agreeing content succeeds", func(t *testing.T) {
		path := writeHostsFile(t, sameIPHosts)

		_, err := execHostctl(t, path, "127.0.0.1 third.loc\n", "add", "profile1", "--strict")
		require.NoError(t, err)
	})

	t.Run("add domains conflicting route is rejected", func(t *testing.T) {
		path := writeHostsFile(t, sameIPHosts)
		before, err := os.ReadFile(path)
		require.NoError(t, err)

		_, err = execHostctl(t, path, "",
			"add", "domains", "profile3", "first.loc", "--ip", "10.0.0.2", "--strict", "--quiet")
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})
}
