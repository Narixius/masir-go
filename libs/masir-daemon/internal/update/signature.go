package update

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"aead.dev/minisign"
)

// publicKeys are the minisign public keys releases are signed with, comma separated
// ("RWQ..."), set at build time (see .goreleaser.yaml). A release is accepted when any
// of them signed it, so a key can be replaced without stranding installed copies. A
// build without keys (a development or snapshot build) can't check signatures.
var publicKeys = ""

// SignatureAsset is the release file holding the signature of checksums.txt.
const SignatureAsset = "checksums.txt.minisig"

var errBadSignature = errors.New("the release's signature doesn't match; it wasn't signed by masir's key, or it was altered")

// trustedKeys parses the keys built into this binary.
func trustedKeys() ([]minisign.PublicKey, error) {
	var keys []minisign.PublicKey
	for _, text := range strings.Split(publicKeys, ",") {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		var k minisign.PublicKey
		if err := k.UnmarshalText([]byte(text)); err != nil {
			return nil, fmt.Errorf("this build has an invalid update signing key: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// checkSignature verifies checksums.txt, the list every download is then checked
// against, so a release can't be altered by whoever can upload files to it. Without
// keys in the build there's nothing to check against and it says so.
func checkSignature(ctx context.Context, c Clients, rel *Release, list []byte) error {
	keys, err := trustedKeys()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		log.Printf("update: this build has no signing key, so %s isn't checked", rel.Tag)
		return nil
	}
	asset, ok := rel.Asset(SignatureAsset)
	if !ok {
		return fmt.Errorf("release %s isn't signed (it has no %s)", rel.Tag, SignatureAsset)
	}
	sig, err := fetchSmall(ctx, c, asset.URL)
	if err != nil {
		return err
	}
	return verifySignature(keys, list, sig)
}

func verifySignature(keys []minisign.PublicKey, message, signature []byte) error {
	for _, k := range keys {
		if minisign.Verify(k, message, signature) {
			return nil
		}
	}
	return errBadSignature
}
