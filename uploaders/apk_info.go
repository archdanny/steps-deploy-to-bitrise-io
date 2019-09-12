package uploaders

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/sliceutil"
	"github.com/bitrise-steplib/steps-xcode-test/pretty"
)

const universalSplitParam = "universal"

// The order of split params matter, while the artifact path parsing is done, we remove the split params in this order.
// If we would remove `xhdpi` from app-xxxhdpi-debug.apk, the remaining part would be: app-xx-debug.apk
var (
	// based on: https://developer.android.com/ndk/guides/abis.html#sa
	abis            = []string{"armeabi-v7a", "arm64-v8a", "x86_64", "x86", universalSplitParam}
	unsupportedAbis = []string{"mips64", "mips", "armeabi"}

	// based on: https://developer.android.com/studio/build/configure-apk-splits#configure-density-split
	screenDensities = []string{"xxxhdpi", "xxhdpi", "xhdpi", "hdpi", "mdpi", "ldpi", "280", "360", "420", "480", "560"}
)

// ArtifactSigningInfo ...
type ArtifactSigningInfo struct {
	Unsigned      bool
	BitriseSigned bool
}

const bitriseSignedSuffix = "-bitrise-signed"
const unsignedSuffix = "-unsigned"

func parseSigningInfo(pth string) (ArtifactSigningInfo, string) {
	info := ArtifactSigningInfo{}

	ext := filepath.Ext(pth)
	base := filepath.Base(pth)
	base = strings.TrimSuffix(base, ext)

	// a given artifact is either:
	// signed: no suffix
	// unsigned: `-unsigned` suffix
	// bitrise signed: `-bitrise-signed` suffix: https://github.com/bitrise-steplib/steps-sign-apk/blob/master/main.go#L411
	if strings.HasSuffix(base, bitriseSignedSuffix) {
		base = strings.TrimSuffix(base, bitriseSignedSuffix)
		info.BitriseSigned = true
	}

	if strings.HasSuffix(base, unsignedSuffix) {
		base = strings.TrimSuffix(base, unsignedSuffix)
		info.Unsigned = true
	}

	return info, base
}

// ArtifactSplitInfo ...
type ArtifactSplitInfo struct {
	SplitParams []string
	Universal   bool
}

func firstLetterUpper(str string) string {
	for i, v := range str {
		return string(unicode.ToUpper(v)) + str[i+1:]
	}
	return ""
}

func parseSplitInfo(flavour string) (ArtifactSplitInfo, string) {
	// 2 flavours + density split: minApi21-full-hdpi
	// density and abi split: hdpiArmeabi
	// flavour + density and abi split: demo-hdpiArm64-v8a
	info := ArtifactSplitInfo{}

	var splitParams []string
	splitParams = append(splitParams, abis...)
	splitParams = append(splitParams, unsupportedAbis...)
	splitParams = append(splitParams, screenDensities...)

	for _, splitParam := range splitParams {
		// in case of density + ABI split the 2. split param starts with upper case letter: demo-hdpiArm64-v8a
		for _, param := range []string{splitParam, firstLetterUpper(splitParam)} {
			if strings.Contains(flavour, param) {
				flavour = strings.Replace(flavour, param, "", 1)

				info.SplitParams = append(info.SplitParams, splitParam)
				if splitParam == universalSplitParam {
					info.Universal = true
				}

				break
			}
		}
	}

	// after removing split params, may leading/trailing - char remains: demo-hdpiArm64-v8a
	flavour = strings.TrimPrefix(flavour, "-")
	return info, strings.TrimSuffix(flavour, "-")
}

// ArtifactInfo ...
type ArtifactInfo struct {
	Module         string
	ProductFlavour string
	BuildType      string

	SigningInfo ArtifactSigningInfo
	SplitInfo   ArtifactSplitInfo
}

func parseArtifactInfo(pth string) ArtifactInfo {
	info := ArtifactInfo{}

	var base string
	info.SigningInfo, base = parseSigningInfo(pth)

	// based on: https://developer.android.com/studio/build/build-variants
	// - <build variant> = <product flavor> + <build type>
	// - debug and release build types always exists
	// - APK/AAB base name layout: <module>-<product flavor?>-<build type>.<apk|aab>
	// - Sample APK path: $BITRISE_DEPLOY_DIR/app-minApi21-demo-hdpi-debug.apk
	s := strings.Split(base, "-")
	if len(s) < 2 {
		// unknown app base name
		// app artifact name can be customized: https://stackoverflow.com/a/28250257
		return info
	}

	info.Module = s[0]
	info.BuildType = s[len(s)-1]
	if len(s) > 2 {
		productFlavourWithSplitParams := strings.Join(s[1:len(s)-1], "-")
		info.SplitInfo, info.ProductFlavour = parseSplitInfo(productFlavourWithSplitParams)

	}
	return info
}

// AndroidArtifactMap module/buildType/flavour/artifacts
type AndroidArtifactMap map[string]map[string]map[string]Artifact

// Artifact ...
type Artifact struct {
	APK string

	AAB          string
	Split        []string
	UniversalApk string
}

// mapBuildArtifacts returns map[module]map[buildType]map[productFlavour]path.
func mapBuildArtifacts(pths []string) AndroidArtifactMap {
	buildArtifacts := map[string]map[string]map[string]Artifact{}
	for _, pth := range pths {
		info := parseArtifactInfo(pth)

		moduleArtifacts, ok := buildArtifacts[info.Module]
		if !ok {
			moduleArtifacts = map[string]map[string]Artifact{}
		}

		buildTypeArtifacts, ok := moduleArtifacts[info.BuildType]
		if !ok {
			buildTypeArtifacts = map[string]Artifact{}
		}

		artifact := buildTypeArtifacts[info.ProductFlavour]

		if filepath.Ext(pth) == ".aab" {
			if len(artifact.AAB) != 0 {
				log.Warnf("Multple AAB generated for module: %s, productFlavour: %s, buildType: %s: %s", info.Module, info.ProductFlavour, info.BuildType, pth)
			}
			artifact.AAB = pth
			buildTypeArtifacts[info.ProductFlavour] = artifact
			moduleArtifacts[info.BuildType] = buildTypeArtifacts
			buildArtifacts[info.Module] = moduleArtifacts
			continue
		}

		if len(info.SplitInfo.SplitParams) == 0 {
			if len(artifact.APK) != 0 {
				// might -unsigned and -bitrise-signed versions both exist of the same apk
			}
			artifact.APK = pth
			buildTypeArtifacts[info.ProductFlavour] = artifact
			moduleArtifacts[info.BuildType] = buildTypeArtifacts
			buildArtifacts[info.Module] = moduleArtifacts
			continue
		}

		if info.SplitInfo.Universal {
			if len(artifact.UniversalApk) != 0 {
				log.Warnf("Multple universal APK generated for module: %s, productFlavour: %s, buildType: %s: %s", info.Module, info.ProductFlavour, info.BuildType, pth)
			}
			artifact.UniversalApk = pth
		}

		// might -unsigned and -bitrise-signed versions of the same apk is listed
		added := false
		for _, suffix := range []string{"", "-unsigned", "-bitrise-signed"} {
			_, base := parseSigningInfo(pth)
			artifactPth := filepath.Join(filepath.Dir(pth), base+suffix+filepath.Ext(pth))

			if sliceutil.IsStringInSlice(artifactPth, artifact.Split) {
				added = true
				break
			}
		}

		if !added {
			artifact.Split = append(artifact.Split, pth)
			buildTypeArtifacts[info.ProductFlavour] = artifact
		}

		moduleArtifacts[info.BuildType] = buildTypeArtifacts
		buildArtifacts[info.Module] = moduleArtifacts
	}

	return buildArtifacts
}

func remove(slice []string, i int) []string {
	return append(slice[:i], slice[i+1:]...)
}

// SplitArtifactMeta ...
type SplitArtifactMeta Artifact

func createSplitArtifactMeta(pth string, pths []string) (SplitArtifactMeta, error) {
	artifactsMap := mapBuildArtifacts(pths)
	info := parseArtifactInfo(pth)

	moduleArtifacts, ok := artifactsMap[info.Module]
	if !ok {
		return SplitArtifactMeta{}, fmt.Errorf("artifact: %s is not part of the artifact mapping: %s", pth, pretty.Object(artifactsMap))
	}

	buildTypeArtifacts, ok := moduleArtifacts[info.BuildType]
	if !ok {
		return SplitArtifactMeta{}, fmt.Errorf("artifact: %s is not part of the artifact mapping: %s", pth, pretty.Object(artifactsMap))
	}

	artifact, ok := buildTypeArtifacts[info.ProductFlavour]
	if !ok {
		return SplitArtifactMeta{}, fmt.Errorf("artifact: %s is not part of the artifact mapping: %s", pth, pretty.Object(artifactsMap))
	}

	return SplitArtifactMeta(artifact), nil
}