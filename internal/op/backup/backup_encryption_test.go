package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	internaldb "github.com/lingyuins/octopus/internal/db"
	"github.com/lingyuins/octopus/internal/model"
	"github.com/lingyuins/octopus/internal/transformer/outbound"
	"github.com/lingyuins/octopus/internal/utils/apikeyhash"
	"github.com/lingyuins/octopus/internal/utils/crypto"
	"gorm.io/gorm"
)

// TestMain initializes the process-wide crypto key for this package's tests.
// Uses the repo-wide shared test key (same constant as other packages' TestMain).
func TestMain(m *testing.M) {
	crypto.Init("octopus-test-encryption-key")
	os.Exit(m.Run())
}

// resetCryptoKeyForTest switches the process crypto key mid-test (simulating a
// different instance with a different encryption_key). t.Cleanup restores the
// shared test key so later tests in the same process are unaffected.
func resetCryptoKeyForTest(t *testing.T, key string) {
	t.Helper()
	crypto.ResetForTest()
	crypto.Init(key)
	t.Cleanup(func() {
		crypto.ResetForTest()
		crypto.Init("octopus-test-encryption-key")
	})
}

func mustEncrypt(t *testing.T, plain string) string {
	t.Helper()
	encrypted, err := crypto.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt %q: %v", plain, err)
	}
	if !crypto.IsEncrypted(encrypted) {
		t.Fatalf("encrypted value %q lost enc: prefix", encrypted)
	}
	return encrypted
}

// initBackupTestDB opens a fresh process-global SQLite DB for one test and
// registers cleanup (same pattern as the other tests in this package).
func initBackupTestDB(t *testing.T, fileName string) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), fileName)
	if err := internaldb.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = internaldb.Close() })
	return internaldb.GetDB()
}

// seedEncryptedCoreRows seeds one channel + channel key, one api key and one
// credential profile, all sensitive values stored as enc: ciphertext.
func seedEncryptedCoreRows(t *testing.T, conn *gorm.DB, plainAPIKey, plainChannelKey, plainCredentialKey string) {
	t.Helper()
	if err := conn.Create(&model.Channel{ID: 1, Name: "ch1", Type: outbound.OutboundTypeOpenAIChat}).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	if err := conn.Create(&model.APIKey{ID: 1, Name: "k1", APIKey: mustEncrypt(t, plainAPIKey), APIKeyHash: apikeyhash.Sum(plainAPIKey)}).Error; err != nil {
		t.Fatalf("seed api_keys: %v", err)
	}
	if err := conn.Create(&model.ChannelKey{ID: 1, ChannelID: 1, ChannelKey: mustEncrypt(t, plainChannelKey)}).Error; err != nil {
		t.Fatalf("seed channel_keys: %v", err)
	}
	if err := conn.Create(&model.APICredentialProfile{ID: 1, Name: "p1", BaseURL: "https://api.example.com", APIKey: mustEncrypt(t, plainCredentialKey)}).Error; err != nil {
		t.Fatalf("seed api_credential_profiles: %v", err)
	}
}

func assertEncryptedRoundTrip(t *testing.T, field, stored, wantPlain string) {
	t.Helper()
	if !crypto.IsEncrypted(stored) {
		t.Fatalf("%s = %q, want enc: ciphertext", field, stored)
	}
	decrypted, err := crypto.Decrypt(stored)
	if err != nil {
		t.Fatalf("%s not decryptable under current key: %v", field, err)
	}
	if decrypted != wantPlain {
		t.Fatalf("%s decrypts to %q, want %q", field, decrypted, wantPlain)
	}
}

// TestExportDecryptsSensitiveFields verifies issue #247: sensitive fields that
// are stored as enc: ciphertext are exported as plaintext, making the backup
// portable across instances with different encryption keys.
func TestExportDecryptsSensitiveFields(t *testing.T) {
	conn := initBackupTestDB(t, "enc_export.db")

	const (
		plainAPIKey  = "sk-plain-api-key-1"
		plainChanKey = "sk-upstream-channel-key-1"
		plainCredKey = "sk-credential-profile-key-1"
	)

	seedEncryptedCoreRows(t, conn, plainAPIKey, plainChanKey, plainCredKey)

	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if dump.Version != 3 {
		t.Fatalf("dump version = %d, want 3 (plaintext secrets, issue #247)", dump.Version)
	}
	if dbDumpVersion != 3 {
		t.Fatalf("dbDumpVersion = %d, want 3", dbDumpVersion)
	}

	assertPlain := func(field, got, want string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s = %q, want plaintext %q", field, got, want)
		}
		if crypto.IsEncrypted(got) {
			t.Fatalf("%s still carries enc: prefix", field)
		}
	}
	assertPlain("api_keys.api_key", dump.APIKeys[0].APIKey, plainAPIKey)
	assertPlain("channel_keys.channel_key", dump.ChannelKeys[0].ChannelKey, plainChanKey)
	assertPlain("api_credential_profiles.api_key", dump.APICredentialProfiles[0].APIKey, plainCredKey)
	if dump.APIKeys[0].APIKeyHash != apikeyhash.Sum(plainAPIKey) {
		t.Fatalf("api_keys.api_key_hash = %q, want unchanged %q", dump.APIKeys[0].APIKeyHash, apikeyhash.Sum(plainAPIKey))
	}
}

// TestExportKeepsUndecryptableValues verifies that a row whose ciphertext
// cannot be decrypted (corrupt payload / key mismatch) does not fail the
// export — the original value is kept as-is.
func TestExportKeepsUndecryptableValues(t *testing.T) {
	conn := initBackupTestDB(t, "enc_undecryptable.db")

	const broken = "enc:!!!not-valid-base64!!!"
	if err := conn.Create(&model.Channel{ID: 1, Name: "ch1", Type: outbound.OutboundTypeOpenAIChat}).Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	if err := conn.Create(&model.ChannelKey{ID: 1, ChannelID: 1, ChannelKey: broken}).Error; err != nil {
		t.Fatalf("seed channel_keys: %v", err)
	}

	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatalf("export should tolerate undecryptable values: %v", err)
	}
	if len(dump.ChannelKeys) != 1 {
		t.Fatalf("exported channel_keys = %d, want 1", len(dump.ChannelKeys))
	}
	if dump.ChannelKeys[0].ChannelKey != broken {
		t.Fatalf("undecryptable channel key = %q, want original %q", dump.ChannelKeys[0].ChannelKey, broken)
	}
}

// TestImportEncryptsPlaintextAndComputesHash verifies the import side of
// issue #247: a v3 dump (plaintext secrets) is re-encrypted on import and
// api_keys rows get their deterministic api_key_hash filled.
func TestImportEncryptsPlaintextAndComputesHash(t *testing.T) {
	conn := initBackupTestDB(t, "enc_import.db")

	const (
		plainAPIKey  = "sk-import-plain-key-1"
		plainChanKey = "sk-import-channel-key-1"
		plainCredKey = "sk-import-credential-key-1"
	)
	dump := &model.DBDump{
		Version: 3,
		Channels: []model.Channel{
			{ID: 1, Name: "ch1", Type: outbound.OutboundTypeOpenAIChat},
		},
		APIKeys: []model.APIKey{
			{ID: 1, Name: "k1", APIKey: plainAPIKey},
		},
		ChannelKeys: []model.ChannelKey{
			{ID: 1, ChannelID: 1, ChannelKey: plainChanKey},
		},
		APICredentialProfiles: []model.APICredentialProfile{
			{ID: 1, Name: "p1", BaseURL: "https://api.example.com", APIKey: plainCredKey},
		},
	}
	if _, err := ImportWithModeToDB(context.Background(), conn, dump, model.ImportModeIncremental); err != nil {
		t.Fatalf("import: %v", err)
	}

	var key model.APIKey
	if err := conn.First(&key, 1).Error; err != nil {
		t.Fatalf("query api_keys: %v", err)
	}
	assertEncryptedRoundTrip(t, "api_keys.api_key", key.APIKey, plainAPIKey)
	if want := apikeyhash.Sum(plainAPIKey); key.APIKeyHash != want {
		t.Fatalf("api_key_hash = %q, want %q", key.APIKeyHash, want)
	}

	var channelKey model.ChannelKey
	if err := conn.First(&channelKey, 1).Error; err != nil {
		t.Fatalf("query channel_keys: %v", err)
	}
	assertEncryptedRoundTrip(t, "channel_keys.channel_key", channelKey.ChannelKey, plainChanKey)

	var profile model.APICredentialProfile
	if err := conn.First(&profile, 1).Error; err != nil {
		t.Fatalf("query api_credential_profiles: %v", err)
	}
	assertEncryptedRoundTrip(t, "api_credential_profiles.api_key", profile.APIKey, plainCredKey)
}

// TestImportIdempotentOnEncryptedValues verifies that a v1/v2 dump whose
// sensitive fields already carry enc: ciphertext (e.g. an old backup restored
// into the same instance) is imported unchanged — no double encryption.
// api_key_hash never travels in old JSON dumps (json:"-"), so rows with an
// empty hash must still get a distinct hash locally; otherwise the unique
// index plus ON CONFLICT DO NOTHING would silently skip every row after the
// first.
func TestImportIdempotentOnEncryptedValues(t *testing.T) {
	conn := initBackupTestDB(t, "enc_idempotent.db")

	const (
		plainKey1 = "sk-idempotent-key-1"
		plainKey2 = "sk-idempotent-key-2"
		plainKey3 = "sk-idempotent-key-3"
	)
	encKey1 := mustEncrypt(t, plainKey1)
	encKey2 := mustEncrypt(t, plainKey2)
	encKey3 := mustEncrypt(t, plainKey3)
	encChannelKey := mustEncrypt(t, "sk-idempotent-channel-key-1")

	dump := &model.DBDump{
		Version: 2, // old dump: fields carry the (same-instance) ciphertext.
		Channels: []model.Channel{
			{ID: 1, Name: "ch1", Type: outbound.OutboundTypeOpenAIChat},
		},
		APIKeys: []model.APIKey{
			{ID: 1, Name: "k1", APIKey: encKey1, APIKeyHash: apikeyhash.Sum(plainKey1)},
			{ID: 2, Name: "k2", APIKey: encKey2},
			{ID: 3, Name: "k3", APIKey: encKey3},
		},
		ChannelKeys: []model.ChannelKey{
			{ID: 1, ChannelID: 1, ChannelKey: encChannelKey},
		},
	}
	if _, err := ImportWithModeToDB(context.Background(), conn, dump, model.ImportModeIncremental); err != nil {
		t.Fatalf("import: %v", err)
	}

	for id, want := range map[int][2]string{
		1: {encKey1, plainKey1},
		2: {encKey2, plainKey2},
		3: {encKey3, plainKey3},
	} {
		wantEnc, wantPlain := want[0], want[1]
		var key model.APIKey
		if err := conn.First(&key, id).Error; err != nil {
			t.Fatalf("query api_keys id=%d: %v", id, err)
		}
		if key.APIKey != wantEnc {
			t.Fatalf("api_keys id=%d was re-encrypted: got %q, want unchanged %q", id, key.APIKey, wantEnc)
		}
		if want := apikeyhash.Sum(wantPlain); key.APIKeyHash != want {
			t.Fatalf("api_keys id=%d hash = %q, want %q", id, key.APIKeyHash, want)
		}
	}

	var channelKey model.ChannelKey
	if err := conn.First(&channelKey, 1).Error; err != nil {
		t.Fatalf("query channel_keys: %v", err)
	}
	if channelKey.ChannelKey != encChannelKey {
		t.Fatalf("encrypted channel key was re-encrypted: got %q, want unchanged %q", channelKey.ChannelKey, encChannelKey)
	}
}

// TestImportLegacyHashedKeyKeepsHashSemantics verifies that a hash-era row
// (api_key column carries the 64-hex hash itself) keeps its live-DB semantics
// after import: the value is not re-encrypted as if it were plaintext, and the
// hash column is filled with the value, so clients holding the original
// plaintext still authenticate via sha256(plaintext) == stored value.
func TestImportLegacyHashedKeyKeepsHashSemantics(t *testing.T) {
	conn := initBackupTestDB(t, "enc_legacy_hash.db")

	const original = "sk-octopus-legacy-plaintext"
	legacyHash := apikeyhash.Sum(original)
	dump := &model.DBDump{
		Version: 2,
		APIKeys: []model.APIKey{
			{ID: 1, Name: "legacy", APIKey: legacyHash},
		},
	}
	if _, err := ImportWithModeToDB(context.Background(), conn, dump, model.ImportModeIncremental); err != nil {
		t.Fatalf("import: %v", err)
	}

	var key model.APIKey
	if err := conn.First(&key, 1).Error; err != nil {
		t.Fatalf("query api_keys: %v", err)
	}
	if key.APIKey != legacyHash {
		t.Fatalf("legacy hashed api_key = %q, want unchanged %q", key.APIKey, legacyHash)
	}
	if key.APIKeyHash != legacyHash {
		t.Fatalf("legacy hashed api_key_hash = %q, want %q", key.APIKeyHash, legacyHash)
	}
}

// TestCrossKeyRoundTrip is the core regression for issue #247: export from an
// instance with key A, switch the process key to key B (simulating a different
// instance), import, and verify the stored ciphertext is decryptable under
// key B back to the original plaintext.
func TestCrossKeyRoundTrip(t *testing.T) {
	const (
		plainAPIKey  = "sk-cross-key-api-key-1"
		plainChanKey = "sk-cross-key-channel-key-1"
		plainCredKey = "sk-cross-key-credential-key-1"
	)

	// --- Instance A: seed encrypted data and export. ---
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	if err := internaldb.InitDB("sqlite", sourcePath, false); err != nil {
		t.Fatalf("init source db: %v", err)
	}
	srcConn := internaldb.GetDB()
	seedEncryptedCoreRows(t, srcConn, plainAPIKey, plainChanKey, plainCredKey)
	dump, err := ExportAll(context.Background(), false, false)
	if err != nil {
		t.Fatalf("export from instance A: %v", err)
	}
	if dump.APIKeys[0].APIKey != plainAPIKey || dump.ChannelKeys[0].ChannelKey != plainChanKey || dump.APICredentialProfiles[0].APIKey != plainCredKey {
		t.Fatalf("dump should carry plaintext secrets: %+v / %+v / %+v", dump.APIKeys[0], dump.ChannelKeys[0], dump.APICredentialProfiles[0])
	}
	// The production flow serializes the dump to JSON and back; api_key_hash is
	// json:"-" and therefore never travels in the file. The import side must
	// backfill it (asserted on the target rows below).
	body, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("marshal dump: %v", err)
	}
	dump = &model.DBDump{}
	if err := json.Unmarshal(body, dump); err != nil {
		t.Fatalf("unmarshal dump: %v", err)
	}
	// Release the source DB file handle (Windows: TempDir cleanup fails on
	// open files). The process-global pointer stays stale afterwards, but this
	// test only uses the standalone target handle from here on.
	if sqlDB, err := srcConn.DB(); err == nil {
		_ = sqlDB.Close()
	}

	// --- Switch to instance B's key (cross-instance import). ---
	resetCryptoKeyForTest(t, "octopus-test-encryption-key-instance-b")

	// --- Instance B: import into a fresh database. ---
	targetPath := filepath.Join(t.TempDir(), "target.db")
	target, err := internaldb.OpenStandalone("sqlite", targetPath, false)
	if err != nil {
		t.Fatalf("open target db: %v", err)
	}
	if err := internaldb.Migrate(target); err != nil {
		t.Fatalf("migrate target db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := target.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if _, err := ImportWithModeToDB(context.Background(), target, dump, model.ImportModeFull); err != nil {
		t.Fatalf("import into instance B: %v", err)
	}

	var key model.APIKey
	if err := target.First(&key, 1).Error; err != nil {
		t.Fatalf("query target api_keys: %v", err)
	}
	assertEncryptedRoundTrip(t, "target api_keys.api_key", key.APIKey, plainAPIKey)
	if want := apikeyhash.Sum(plainAPIKey); key.APIKeyHash != want {
		t.Fatalf("target api_key_hash = %q, want %q", key.APIKeyHash, want)
	}

	var channelKey model.ChannelKey
	if err := target.First(&channelKey, 1).Error; err != nil {
		t.Fatalf("query target channel_keys: %v", err)
	}
	assertEncryptedRoundTrip(t, "target channel_keys.channel_key", channelKey.ChannelKey, plainChanKey)

	var profile model.APICredentialProfile
	if err := target.First(&profile, 1).Error; err != nil {
		t.Fatalf("query target api_credential_profiles: %v", err)
	}
	assertEncryptedRoundTrip(t, "target api_credential_profiles.api_key", profile.APIKey, plainCredKey)
}
