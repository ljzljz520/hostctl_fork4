package file

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guumaster/hostctl/pkg/parser"
	"github.com/guumaster/hostctl/pkg/types"
)

const strictHostsPath = "/tmp/etc/hosts"

func createStrictFS(t *testing.T, content string) afero.Fs {
	t.Helper()

	appFS := afero.NewMemMapFs()
	require.NoError(t, appFS.MkdirAll("/tmp/etc", 0755))

	f, err := appFS.Create(strictHostsPath)
	require.NoError(t, err)

	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	return appFS
}

// conflictingFixture has profile1 enabled and profile2 disabled mapping the
// same domain to a different IPv4 address.
const conflictingFixture = `
127.0.0.1 localhost
` + Banner + `
# profile.on profile1
127.0.0.1 first.loc
# end
# profile.off profile2
# 10.0.0.2 first.loc
# end
`

const sameIPFixture = `
127.0.0.1 localhost
` + Banner + `
# profile.on profile1
127.0.0.1 first.loc
# end
# profile.off profile2
# 127.0.0.1 first.loc
# end
`

func openStrictFile(t *testing.T, mem afero.Fs) *File {
	t.Helper()

	f, err := NewWithFs(strictHostsPath, mem)
	require.NoError(t, err)
	f.SetStrict(true)

	return f
}

func TestFile_StrictEnable(t *testing.T) {
	t.Run("conflicting enable is rejected and rolled back", func(t *testing.T) {
		mem := createStrictFS(t, conflictingFixture)

		m := openStrictFile(t, mem)

		err := m.Enable([]string{"profile2"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))
		assert.Contains(t, err.Error(), "first.loc")
		assert.Contains(t, err.Error(), "127.0.0.1")
		assert.Contains(t, err.Error(), "10.0.0.2")
		assert.Contains(t, err.Error(), "profile1")
		assert.Contains(t, err.Error(), "profile2")

		// in-memory state rolled back: profile2 stays disabled
		assert.NotContains(t, m.GetEnabled(), "profile2")
		assert.Contains(t, m.GetDisabled(), "profile2")

		// the rolled-back state is valid again, so flushing rewrites the
		// same logical content (rendering normalizes blank lines)
		require.NoError(t, m.Flush())

		reloaded, err := NewWithFs(strictHostsPath, mem)
		require.NoError(t, err)
		assert.Contains(t, reloaded.GetDisabled(), "profile2")
		assert.NotContains(t, reloaded.GetEnabled(), "profile2")

		p2, err := reloaded.GetProfile("profile2")
		require.NoError(t, err)
		hosts, err := p2.GetHostNames("10.0.0.2")
		require.NoError(t, err)
		assert.Equal(t, []string{"first.loc"}, hosts)

		after, err := afero.ReadFile(mem, strictHostsPath)
		require.NoError(t, err)
		assert.Contains(t, string(after), "# profile.off profile2")
		assert.Contains(t, string(after), "# 10.0.0.2 first.loc")
	})

	t.Run("flush refuses when the in-memory set still conflicts", func(t *testing.T) {
		mem := createStrictFS(t, conflictingFixture)
		before, err := afero.ReadFile(mem, strictHostsPath)
		require.NoError(t, err)

		m := openStrictFile(t, mem)

		// library-style direct mutation bypassing the guarded operations
		p, err := m.GetProfile("profile2")
		require.NoError(t, err)
		p.Status = types.Enabled

		err = m.Flush()
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		err = m.WriteTo("/tmp/etc/hosts.copy")
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		_, statErr := mem.Stat("/tmp/etc/hosts.copy")
		assert.Error(t, statErr)

		after, err := afero.ReadFile(mem, strictHostsPath)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})

	t.Run("same IP duplicate sources can be enabled", func(t *testing.T) {
		mem := createStrictFS(t, sameIPFixture)
		m := openStrictFile(t, mem)

		require.NoError(t, m.Enable([]string{"profile2"}))
		assert.Contains(t, m.GetEnabled(), "profile1")
		assert.Contains(t, m.GetEnabled(), "profile2")
		require.NoError(t, m.Flush())
	})

	t.Run("IPv4/IPv6 pair can be enabled", func(t *testing.T) {
		fixture := `
127.0.0.1 localhost
` + Banner + `
# profile.on profile1
127.0.0.1 first.loc
# end
# profile.off profile2
# ::1 first.loc
# end
`
		mem := createStrictFS(t, fixture)
		m := openStrictFile(t, mem)

		require.NoError(t, m.Enable([]string{"profile2"}))
		assert.Contains(t, m.GetEnabled(), "profile2")
	})

	t.Run("enable --all with a conflict is atomic", func(t *testing.T) {
		mem := createStrictFS(t, conflictingFixture)
		m := openStrictFile(t, mem)

		err := m.EnableAll()
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))
		assert.Contains(t, m.GetDisabled(), "profile2")
	})

	t.Run("disabling can never produce a conflict", func(t *testing.T) {
		mem := createStrictFS(t, conflictingFixture)
		m := openStrictFile(t, mem)

		require.NoError(t, m.Disable([]string{"profile1"}))
		assert.Contains(t, m.GetDisabled(), "profile1")
	})
}

func TestFile_StrictToggle(t *testing.T) {
	// profile1 on 127.0.0.1, profile2 off 10.0.0.2, profile3 off 127.0.0.1
	const batchFixture = `
127.0.0.1 localhost
` + Banner + `
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

	t.Run("batch toggle is atomic on conflict", func(t *testing.T) {
		mem := createStrictFS(t, batchFixture)
		before, err := afero.ReadFile(mem, strictHostsPath)
		require.NoError(t, err)

		m := openStrictFile(t, mem)

		// enabling profile2 (10.0.0.2) and profile3 (127.0.0.1) at once
		// leaves three disagreeing enabled profiles
		err = m.Toggle([]string{"profile2", "profile3"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		// neither toggle of the batch is committed
		assert.Contains(t, m.GetEnabled(), "profile1")
		assert.Contains(t, m.GetDisabled(), "profile2")
		assert.Contains(t, m.GetDisabled(), "profile3")

		after, err := afero.ReadFile(mem, strictHostsPath)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})

	t.Run("single toggle that would conflict is rejected", func(t *testing.T) {
		mem := createStrictFS(t, conflictingFixture)
		m := openStrictFile(t, mem)

		err := m.Toggle([]string{"profile2"})
		require.Error(t, err)
		assert.Contains(t, m.GetDisabled(), "profile2")
	})

	t.Run("toggling away keeps the active set consistent", func(t *testing.T) {
		mem := createStrictFS(t, conflictingFixture)
		m := openStrictFile(t, mem)

		require.NoError(t, m.Toggle([]string{"profile1"}))
		assert.Contains(t, m.GetDisabled(), "profile1")
		assert.Contains(t, m.GetDisabled(), "profile2")
	})

	t.Run("unknown profile in batch rolls statuses back", func(t *testing.T) {
		mem := createStrictFS(t, conflictingFixture)
		m := openStrictFile(t, mem)

		err := m.Toggle([]string{"profile2", "unknown"})
		require.ErrorIs(t, err, types.ErrUnknownProfile)
		assert.Contains(t, m.GetDisabled(), "profile2")
	})
}

func TestFile_StrictOnly(t *testing.T) {
	// profile1 on 127.0.0.1, profile2 off 10.0.0.2, profile3 off 127.0.0.1
	fixture := `
127.0.0.1 localhost
` + Banner + `
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

	t.Run("enable only a disagreeing profile is fine alone", func(t *testing.T) {
		mem := createStrictFS(t, fixture)
		m := openStrictFile(t, mem)

		require.NoError(t, m.EnableOnly([]string{"profile2"}))
		assert.Contains(t, m.GetEnabled(), "profile2")
		assert.Contains(t, m.GetDisabled(), "profile1")
		assert.Contains(t, m.GetDisabled(), "profile3")
	})

	t.Run("enable only final set with a conflict is rolled back", func(t *testing.T) {
		mem := createStrictFS(t, fixture)
		m := openStrictFile(t, mem)

		require.NoError(t, m.EnableOnly([]string{"profile2"}))

		err := m.EnableOnly([]string{"profile2", "profile3"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		// state of the previous successful operation is preserved
		assert.Contains(t, m.GetEnabled(), "profile2")
		assert.Contains(t, m.GetDisabled(), "profile3")
		assert.Contains(t, m.GetDisabled(), "profile1")
	})

	t.Run("disable only enabling disagreeing profiles is rolled back", func(t *testing.T) {
		mem := createStrictFS(t, fixture)
		m := openStrictFile(t, mem)

		// disabling only profile1 enables every other profile, and
		// profile2 (10.0.0.2) disagrees with profile3 (127.0.0.1)
		err := m.DisableOnly([]string{"profile1"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		// rollback restores the original statuses
		assert.Contains(t, m.GetEnabled(), "profile1")
		assert.Contains(t, m.GetDisabled(), "profile2")
		assert.Contains(t, m.GetDisabled(), "profile3")
	})
}

func TestFile_StrictReplace(t *testing.T) {
	// Mirrors what `replace` and `sync docker|docker-compose` do: replace the
	// whole profile, then validate the final set of enabled profiles.
	t.Run("sync-style replacement introducing a conflict is rolled back", func(t *testing.T) {
		fixture := `
127.0.0.1 localhost
` + Banner + `
# profile.on profile1
127.0.0.1 first.loc
# end
# profile.on profile2
127.0.0.1 first.loc
# end
`
		mem := createStrictFS(t, fixture)
		before, err := afero.ReadFile(mem, strictHostsPath)
		require.NoError(t, err)

		m := openStrictFile(t, mem)

		p, err := parser.ParseProfile(strings.NewReader("10.0.0.2 first.loc"))
		require.NoError(t, err)
		p.Name = "profile2"
		p.Status = types.Enabled

		err = m.ReplaceProfile(p)
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))

		// old profile2 content still in memory
		old, err := m.GetProfile("profile2")
		require.NoError(t, err)
		hosts, err := old.GetHostNames("127.0.0.1")
		require.NoError(t, err)
		assert.Equal(t, []string{"first.loc"}, hosts)
		_, err = old.GetHostNames("10.0.0.2")
		assert.ErrorIs(t, err, types.ErrNotPresentIP)

		after, err := afero.ReadFile(mem, strictHostsPath)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})

	t.Run("replacement agreeing with enabled profiles succeeds", func(t *testing.T) {
		mem := createStrictFS(t, sameIPFixture)
		m := openStrictFile(t, mem)

		p, err := parser.ParseProfile(strings.NewReader("127.0.0.1 first.loc"))
		require.NoError(t, err)
		p.Name = "profile2"
		p.Status = types.Enabled

		require.NoError(t, m.ReplaceProfile(p))
	})
}

func TestFile_StrictAdd(t *testing.T) {
	t.Run("new profile with a conflicting address is not added", func(t *testing.T) {
		mem := createStrictFS(t, sameIPFixture)
		m := openStrictFile(t, mem)

		err := m.AddRoutes("profile3", []*types.Route{types.NewRoute("10.0.0.2", "first.loc")})
		require.Error(t, err)
		assert.True(t, errors.Is(err, types.ErrStrictConflict))
		assert.NotContains(t, m.GetProfileNames(), "profile3")
	})

	t.Run("adding a matching address to an enabled profile is allowed", func(t *testing.T) {
		mem := createStrictFS(t, sameIPFixture)
		m := openStrictFile(t, mem)

		require.NoError(t, m.AddRoutes("profile1", []*types.Route{types.NewRoute("127.0.0.1", "third.loc")}))
	})

	t.Run("adding an IPv6 beside an IPv4 mapping is allowed", func(t *testing.T) {
		mem := createStrictFS(t, sameIPFixture)
		m := openStrictFile(t, mem)

		require.NoError(t, m.AddRoutes("profile3", []*types.Route{types.NewRoute("::1", "first.loc")}))
	})

	t.Run("default profile name is still rejected before validation", func(t *testing.T) {
		mem := createStrictFS(t, sameIPFixture)
		m := openStrictFile(t, mem)

		p := &types.Profile{Name: types.Default, Status: types.Enabled}
		err := m.AddProfile(p)
		assert.ErrorIs(t, err, types.ErrDefaultProfile)
	})
}

func TestFile_StrictDisabledByDefault(t *testing.T) {
	mem := createStrictFS(t, conflictingFixture)

	f, err := NewWithFs(strictHostsPath, mem)
	require.NoError(t, err)

	// lenient default: conflicting activation keeps working as before
	require.NoError(t, f.Enable([]string{"profile2"}))
	assert.Contains(t, f.GetEnabled(), "profile2")
}
