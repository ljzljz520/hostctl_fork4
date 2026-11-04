package types

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strictProfile(name string, status Status, routes ...*Route) *Profile {
	p := &Profile{
		Name:   name,
		Status: status,
		Routes: map[string]*Route{},
	}
	p.AddRoutes(routes)

	return p
}

func strictContent(profiles ...*Profile) *Content {
	c := &Content{
		ProfileNames: []string{},
		Profiles:     map[string]*Profile{},
	}

	for _, p := range profiles {
		c.ProfileNames = append(c.ProfileNames, p.Name)
		c.Profiles[p.Name] = p
	}

	return c
}

func TestValidateEnabledProfiles(t *testing.T) {
	t.Run("nil content", func(t *testing.T) {
		assert.NoError(t, ValidateEnabledProfiles(nil))
	})

	t.Run("empty content", func(t *testing.T) {
		assert.NoError(t, ValidateEnabledProfiles(strictContent()))
	})

	t.Run("same IP duplicated across profiles is allowed", func(t *testing.T) {
		c := strictContent(
			strictProfile("web", Enabled, NewRoute("127.0.0.1", "app.loc")),
			strictProfile("vpn", Enabled, NewRoute("127.0.0.1", "app.loc")),
		)

		assert.NoError(t, ValidateEnabledProfiles(c))
	})

	t.Run("many domains on one IP is allowed", func(t *testing.T) {
		c := strictContent(
			strictProfile("web", Enabled, NewRoute("127.0.0.1", "a.loc", "b.loc", "c.loc")),
		)

		assert.NoError(t, ValidateEnabledProfiles(c))
	})

	t.Run("IPv4/IPv6 pair across profiles is allowed", func(t *testing.T) {
		c := strictContent(
			strictProfile("web", Enabled, NewRoute("127.0.0.1", "app.loc")),
			strictProfile("vpn", Enabled, NewRoute("::1", "app.loc")),
		)

		assert.NoError(t, ValidateEnabledProfiles(c))
	})

	t.Run("intentional multiple addresses in one profile is allowed", func(t *testing.T) {
		c := strictContent(
			strictProfile("rr", Enabled,
				NewRoute("1.1.1.1", "app.loc"),
				NewRoute("2.2.2.2", "app.loc"),
			),
		)

		assert.NoError(t, ValidateEnabledProfiles(c))
	})

	t.Run("same multi-address set duplicated across profiles is allowed", func(t *testing.T) {
		c := strictContent(
			strictProfile("a", Enabled,
				NewRoute("1.1.1.1", "app.loc"),
				NewRoute("2.2.2.2", "app.loc"),
			),
			strictProfile("b", Enabled,
				NewRoute("1.1.1.1", "app.loc"),
				NewRoute("2.2.2.2", "app.loc"),
			),
		)

		assert.NoError(t, ValidateEnabledProfiles(c))
	})

	t.Run("disabled profiles are ignored", func(t *testing.T) {
		c := strictContent(
			strictProfile("web", Enabled, NewRoute("127.0.0.1", "app.loc")),
			strictProfile("vpn", Disabled, NewRoute("10.0.0.2", "app.loc")),
		)

		assert.NoError(t, ValidateEnabledProfiles(c))
	})

	t.Run("unmanaged default profile is ignored", func(t *testing.T) {
		c := strictContent(
			strictProfile(Default, Enabled, NewRoute("127.0.0.1", "app.loc")),
			strictProfile("web", Enabled, NewRoute("10.0.0.2", "app.loc")),
		)

		assert.NoError(t, ValidateEnabledProfiles(c))
	})

	t.Run("different IPv4 across enabled profiles conflicts", func(t *testing.T) {
		c := strictContent(
			strictProfile("web", Enabled, NewRoute("127.0.0.1", "app.loc")),
			strictProfile("vpn", Enabled, NewRoute("10.0.0.2", "app.loc")),
		)

		err := ValidateEnabledProfiles(c)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrStrictConflict))

		var ce *StrictConflictsError
		require.True(t, errors.As(err, &ce))
		require.Len(t, ce.Conflicts, 1)

		conflict := ce.Conflicts[0]
		assert.Equal(t, "app.loc", conflict.Host)
		assert.Equal(t, familyIPv4, conflict.Family)
		require.Len(t, conflict.Mappings, 2)
		assert.Equal(t, "127.0.0.1", conflict.Mappings[0].IP)
		assert.Equal(t, []string{"web"}, conflict.Mappings[0].Profiles)
		assert.Equal(t, "10.0.0.2", conflict.Mappings[1].IP)
		assert.Equal(t, []string{"vpn"}, conflict.Mappings[1].Profiles)

		msg := err.Error()
		assert.Contains(t, msg, "app.loc")
		assert.Contains(t, msg, "127.0.0.1")
		assert.Contains(t, msg, "10.0.0.2")
		assert.Contains(t, msg, "web")
		assert.Contains(t, msg, "vpn")
		assert.Contains(t, msg, "hosts file not modified")
	})

	t.Run("different IPv6 across enabled profiles conflicts", func(t *testing.T) {
		c := strictContent(
			strictProfile("web", Enabled, NewRoute("::1", "app.loc")),
			strictProfile("vpn", Enabled, NewRoute("fd00::2", "app.loc")),
		)

		err := ValidateEnabledProfiles(c)
		require.Error(t, err)

		var ce *StrictConflictsError
		require.True(t, errors.As(err, &ce))
		require.Len(t, ce.Conflicts, 1)
		assert.Equal(t, familyIPv6, ce.Conflicts[0].Family)
		assert.Equal(t, "::1", ce.Conflicts[0].Mappings[0].IP)
		assert.Equal(t, "fd00::2", ce.Conflicts[0].Mappings[1].IP)
	})

	t.Run("IPv6 conflict is reported even when IPv4 pair agrees", func(t *testing.T) {
		c := strictContent(
			strictProfile("web", Enabled,
				NewRoute("127.0.0.1", "app.loc"),
				NewRoute("::1", "app.loc"),
			),
			strictProfile("vpn", Enabled,
				NewRoute("127.0.0.1", "app.loc"),
				NewRoute("fd00::2", "app.loc"),
			),
		)

		err := ValidateEnabledProfiles(c)
		require.Error(t, err)

		var ce *StrictConflictsError
		require.True(t, errors.As(err, &ce))
		require.Len(t, ce.Conflicts, 1)
		assert.Equal(t, familyIPv6, ce.Conflicts[0].Family)
	})

	t.Run("partial overlap of multi-address sets conflicts", func(t *testing.T) {
		// web maps app.loc to 1.1.1.1 and 2.2.2.2, vpn only knows 1.1.1.1:
		// enabling both changes what vpn users get for the domain.
		c := strictContent(
			strictProfile("web", Enabled,
				NewRoute("1.1.1.1", "app.loc"),
				NewRoute("2.2.2.2", "app.loc"),
			),
			strictProfile("vpn", Enabled, NewRoute("1.1.1.1", "app.loc")),
		)

		err := ValidateEnabledProfiles(c)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrStrictConflict))
	})

	t.Run("multiple conflicting domains are all reported in order", func(t *testing.T) {
		c := strictContent(
			strictProfile("web", Enabled,
				NewRoute("127.0.0.1", "a.loc"),
				NewRoute("127.0.0.1", "b.loc"),
			),
			strictProfile("vpn", Enabled,
				NewRoute("10.0.0.2", "a.loc"),
				NewRoute("10.0.0.3", "b.loc"),
			),
		)

		err := ValidateEnabledProfiles(c)
		require.Error(t, err)

		var ce *StrictConflictsError
		require.True(t, errors.As(err, &ce))
		require.Len(t, ce.Conflicts, 2)
		assert.Equal(t, "a.loc", ce.Conflicts[0].Host)
		assert.Equal(t, "b.loc", ce.Conflicts[1].Host)
	})

	t.Run("same IP repeated on three profiles plus one outlier conflicts", func(t *testing.T) {
		c := strictContent(
			strictProfile("a", Enabled, NewRoute("1.1.1.1", "app.loc")),
			strictProfile("b", Enabled, NewRoute("1.1.1.1", "app.loc")),
			strictProfile("c", Enabled, NewRoute("2.2.2.2", "app.loc")),
		)

		err := ValidateEnabledProfiles(c)
		require.Error(t, err)

		var ce *StrictConflictsError
		require.True(t, errors.As(err, &ce))
		require.Len(t, ce.Conflicts, 1)

		mappings := ce.Conflicts[0].Mappings
		require.Len(t, mappings, 2)
		assert.Equal(t, []string{"a", "b"}, mappings[0].Profiles)
		assert.Equal(t, []string{"c"}, mappings[1].Profiles)
	})
}
