package fullnode

import (
	_ "embed"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	cosmosv1 "github.com/strangelove-ventures/cosmos-operator/api/v1"
	"github.com/strangelove-ventures/cosmos-operator/internal/kube"
	"github.com/strangelove-ventures/cosmos-operator/internal/test"
	"github.com/stretchr/testify/require"
)

var (
	//go:embed testdata/tendermint.toml
	wantTendermint string
	//go:embed testdata/tendermint_defaults.toml
	wantTendermintDefaults string
	//go:embed testdata/tendermint_overrides.toml
	wantTendermintOverrides string

	//go:embed testdata/app.toml
	wantApp string
	//go:embed testdata/app_defaults.toml
	wantAppDefaults string
	//go:embed testdata/app_overrides.toml
	wantAppOverrides string
)

func TestBuildConfigMaps(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		crd := defaultCRD()
		crd.Spec.Replicas = 3
		crd.Name = "agoric"
		crd.Namespace = "test"
		crd.Spec.PodTemplate.Image = "agoric:v6.0.0"
		crd.Spec.ChainSpec.Network = "testnet"

		cms, err := BuildConfigMaps(&crd, nil)
		require.NoError(t, err)
		require.Equal(t, 3, len(cms))

		cm := cms[0]
		require.Equal(t, "agoric-0", cm.Name)
		require.Equal(t, "test", cm.Namespace)
		require.Nil(t, cm.Immutable)

		require.NotEmpty(t, cm.Labels[kube.RevisionLabel])
		delete(cm.Labels, kube.RevisionLabel)

		wantLabels := map[string]string{
			"app.kubernetes.io/created-by": "cosmos-operator",
			"app.kubernetes.io/component":  "CosmosFullNode",
			"app.kubernetes.io/name":       "agoric",
			"app.kubernetes.io/instance":   "agoric-0",
			"app.kubernetes.io/version":    "v6.0.0",
			"cosmos.strange.love/network":  "testnet",
		}

		require.Equal(t, wantLabels, cm.Labels)

		cm = cms[1]
		require.Equal(t, "agoric-1", cm.Name)

		require.NotEmpty(t, cms[0].Data)
		require.Equal(t, cms[0].Data, cms[1].Data)
	})

	t.Run("long name", func(t *testing.T) {
		crd := defaultCRD()
		crd.Spec.Replicas = 3
		crd.Name = strings.Repeat("chain", 300)
		crd.Spec.ChainSpec.Network = strings.Repeat("network", 300)

		cms, err := BuildConfigMaps(&crd, nil)
		require.NoError(t, err)
		require.NotEmpty(t, cms)

		for _, cm := range cms {
			test.RequireValidMetadata(t, cm)
		}
	})

	t.Run("config-overlay.toml", func(t *testing.T) {
		crd := defaultCRD()
		crd.Name = "osmosis"
		crd.Spec.ChainSpec.Network = "mainnet"
		crd.Spec.Replicas = 1
		crd.Spec.ChainSpec.Tendermint = cosmosv1.TendermintConfig{
			PersistentPeers: "peer1@1.2.2.2:789,peer2@2.2.2.2:789,peer3@3.2.2.2:789",
			Seeds:           "seed1@1.1.1.1:456,seed2@1.1.1.1:456",
		}

		t.Run("happy path", func(t *testing.T) {
			custom := crd.DeepCopy()

			custom.Spec.ChainSpec.LogLevel = ptr("debug")
			custom.Spec.ChainSpec.LogFormat = ptr("json")
			custom.Spec.ChainSpec.Tendermint.CorsAllowedOrigins = []string{"*"}
			custom.Spec.ChainSpec.Tendermint.MaxInboundPeers = ptr(int32(5))
			custom.Spec.ChainSpec.Tendermint.MaxOutboundPeers = ptr(int32(15))

			cms, err := BuildConfigMaps(custom, nil)
			require.NoError(t, err)

			cm := cms[0]

			require.NotEmpty(t, cm.Data)
			require.Empty(t, cm.BinaryData)

			var (
				got  map[string]any
				want map[string]any
			)
			_, err = toml.Decode(wantTendermint, &want)
			require.NoError(t, err)

			_, err = toml.Decode(cm.Data["config-overlay.toml"], &got)
			require.NoError(t, err)

			require.Equal(t, want, got)
		})

		t.Run("defaults", func(t *testing.T) {
			cms, err := BuildConfigMaps(&crd, nil)
			require.NoError(t, err)

			cm := cms[0]

			var (
				got  map[string]any
				want map[string]any
			)
			_, err = toml.Decode(wantTendermintDefaults, &want)
			require.NoError(t, err)

			_, err = toml.Decode(cm.Data["config-overlay.toml"], &got)
			require.NoError(t, err)

			require.Equal(t, want, got)
		})

		t.Run("validator sentry", func(t *testing.T) {
			sentry := crd.DeepCopy()
			sentry.Spec.Type = cosmosv1.FullNodeSentry
			cms, err := BuildConfigMaps(sentry, nil)
			require.NoError(t, err)

			cm := cms[0]

			var got map[string]any
			_, err = toml.Decode(cm.Data["config-overlay.toml"], &got)
			require.NoError(t, err)
			require.NotEmpty(t, got)

			require.Equal(t, "tcp://0.0.0.0:1234", got["priv_validator_laddr"])
			require.Equal(t, "null", got["tx_index"].(map[string]any)["indexer"])
		})

		t.Run("overrides", func(t *testing.T) {
			overrides := crd.DeepCopy()
			overrides.Spec.ChainSpec.Tendermint.CorsAllowedOrigins = []string{"should not see me"}
			overrides.Spec.ChainSpec.Tendermint.TomlOverrides = ptr(`
	log_format = "json"
	new_base = "new base value"
	
	[p2p]
	external_address = "override.example.com"
	seeds = "override@seed"
	new_field = "p2p"
	
	[rpc]
	cors_allowed_origins = ["override"]
	
	[new_section]
	test = "value"
	
	[tx_index]
	indexer = "null"
	`)

			p2p := ExternalAddresses{"osmosis-0": "should not see me"}
			cms, err := BuildConfigMaps(overrides, p2p)
			require.NoError(t, err)

			cm := cms[0]

			var (
				got  map[string]any
				want map[string]any
			)
			_, err = toml.Decode(wantTendermintOverrides, &want)
			require.NoError(t, err)

			_, err = toml.Decode(cm.Data["config-overlay.toml"], &got)
			require.NoError(t, err)

			require.Equal(t, want, got)
		})

		t.Run("p2p external addresses", func(t *testing.T) {
			p2p := ExternalAddresses{
				"osmosis-0": "1.1.1.1",
				"osmosis-1": "2.2.2.2",
				"osmosis-2": "3.3.3.3",
			}
			p2pCrd := crd.DeepCopy()
			p2pCrd.Spec.Replicas = 3
			cms, err := BuildConfigMaps(p2pCrd, p2p)
			require.NoError(t, err)

			require.Equal(t, 3, len(cms))

			var decoded decodedToml
			_, err = toml.Decode(cms[0].Data["config-overlay.toml"], &decoded)
			require.NoError(t, err)
			require.Equal(t, "1.1.1.1", decoded["p2p"].(decodedToml)["external_address"])

			_, err = toml.Decode(cms[2].Data["config-overlay.toml"], &decoded)
			require.NoError(t, err)
			require.Equal(t, "3.3.3.3", decoded["p2p"].(decodedToml)["external_address"])
		})

		t.Run("invalid toml", func(t *testing.T) {
			malformed := crd.DeepCopy()
			malformed.Spec.ChainSpec.Tendermint.TomlOverrides = ptr(`invalid_toml = should be invalid`)
			_, err := BuildConfigMaps(malformed, nil)

			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid toml in tendermint overrides")
		})
	})

	t.Run("app-overlay.toml", func(t *testing.T) {
		crd := defaultCRD()
		crd.Spec.Replicas = 3
		crd.Spec.ChainSpec.App = cosmosv1.SDKAppConfig{
			MinGasPrice: "0.123token",
		}

		t.Run("happy path", func(t *testing.T) {
			custom := crd.DeepCopy()
			custom.Spec.ChainSpec.App.APIEnableUnsafeCORS = true
			custom.Spec.ChainSpec.App.GRPCWebEnableUnsafeCORS = true
			custom.Spec.ChainSpec.App.HaltHeight = ptr(uint64(34567))
			custom.Spec.ChainSpec.App.Pruning = &cosmosv1.Pruning{
				Strategy:        "custom",
				Interval:        ptr(uint32(222)),
				KeepEvery:       ptr(uint32(333)),
				KeepRecent:      ptr(uint32(444)),
				MinRetainBlocks: ptr(uint32(271500)),
			}

			cms, err := BuildConfigMaps(custom, nil)
			require.NoError(t, err)

			cm := cms[0]

			require.NotEmpty(t, cm.Data)
			require.Empty(t, cm.BinaryData)

			var (
				got  map[string]any
				want map[string]any
			)
			_, err = toml.Decode(wantApp, &want)
			require.NoError(t, err)

			_, err = toml.Decode(cm.Data["app-overlay.toml"], &got)
			require.NoError(t, err)

			require.Equal(t, want, got)
		})

		t.Run("defaults", func(t *testing.T) {
			cms, err := BuildConfigMaps(&crd, nil)
			require.NoError(t, err)

			cm := cms[0]

			var (
				got  map[string]any
				want map[string]any
			)
			_, err = toml.Decode(wantAppDefaults, &want)
			require.NoError(t, err)

			_, err = toml.Decode(cm.Data["app-overlay.toml"], &got)
			require.NoError(t, err)

			require.Equal(t, want, got)
		})

		t.Run("overrides", func(t *testing.T) {
			overrides := crd.DeepCopy()
			overrides.Spec.ChainSpec.App.MinGasPrice = "should not see me"
			overrides.Spec.ChainSpec.App.TomlOverrides = ptr(`
	minimum-gas-prices = "0.1override"
	new-base = "new base value"
	
	[api]
	enable = false
	new-field = "test"
	`)
			cms, err := BuildConfigMaps(overrides, nil)
			require.NoError(t, err)

			cm := cms[0]

			var (
				got  map[string]any
				want map[string]any
			)
			_, err = toml.Decode(wantAppOverrides, &want)
			require.NoError(t, err)

			_, err = toml.Decode(cm.Data["app-overlay.toml"], &got)
			require.NoError(t, err)

			require.Equal(t, want, got)
		})

		t.Run("invalid toml", func(t *testing.T) {
			malformed := crd.DeepCopy()
			malformed.Spec.ChainSpec.App.TomlOverrides = ptr(`invalid_toml = should be invalid`)
			_, err := BuildConfigMaps(malformed, nil)

			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid toml in app overrides")
		})
	})
}

func FuzzBuildConfigMaps(f *testing.F) {
	crd := defaultCRD()
	f.Add("gaiad", "abcd@1.2.3.4:26656")
	f.Fuzz(func(t *testing.T, binary, peers string) {
		crd.Spec.ChainSpec.ChainID = binary
		crd.Spec.ChainSpec.Tendermint.PersistentPeers = peers
		crd.Spec.Replicas = 1
		cms1, err := BuildConfigMaps(&crd, nil)
		require.NoError(t, err)
		cms2, err := BuildConfigMaps(&crd, nil)
		require.NoError(t, err)

		require.NotEmpty(t, cms1[0].Labels[kube.RevisionLabel])
		require.NotEmpty(t, cms2[0].Labels[kube.RevisionLabel])

		require.Equal(t, cms1[0].Labels[kube.RevisionLabel], cms2[0].Labels[kube.RevisionLabel])

		crd.Spec.PodTemplate.Image = "new-image:v3.4.5"
		cms3, err := BuildConfigMaps(&crd, nil)
		require.NoError(t, err)
		require.NotEqual(t, cms1[0].Labels[kube.RevisionLabel], cms3[0].Labels[kube.RevisionLabel])

		cms4, err := BuildConfigMaps(&crd, ExternalAddresses{"test": "value"})
		require.NoError(t, err)
		require.NotEqual(t, cms3[0].Labels[kube.RevisionLabel], cms4[0].Labels[kube.RevisionLabel])

		crd.Spec.Type = cosmosv1.FullNodeSentry

		cms5, err := BuildConfigMaps(&crd, ExternalAddresses{"test": "value"})
		require.NoError(t, err)
		require.NotEqual(t, cms4[0].Labels[kube.RevisionLabel], cms5[0].Labels[kube.RevisionLabel])
	})
}
