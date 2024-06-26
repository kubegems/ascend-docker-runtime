/* Copyright(C) 2022. Huawei Technologies Co.,Ltd. All rights reserved.
   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

// Package main
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/containerd/containerd/oci"
	"github.com/opencontainers/runtime-spec/specs-go"
	"huawei.com/npu-exporter/v5/common-utils/hwlog"

	"main/dcmi"
	"mindxcheckutils"
)

const (
	runLogPath          = "/var/log/ascend-docker-runtime/runtime-run.log"
	hookDefaultFilePath = "/usr/local/bin/ascend-docker-hook"

	maxCommandLength = 65535
	hookCli          = "ascend-docker-hook"
	destroyHookCli   = "ascend-docker-destroy"
	dockerRuncFile   = "docker-runc"
	runcFile         = "runc"
	envLength        = 2
	kvPairSize       = 2
	borderNum        = 2

	// ENV for device-plugin to identify ascend-docker-runtime
	useAscendDocker      = "ASCEND_DOCKER_RUNTIME=True"
	devicePlugin         = "ascend-device-plugin"
	ascendVisibleDevices = "ASCEND_VISIBLE_DEVICES"
	ascendRuntimeOptions = "ASCEND_RUNTIME_OPTIONS"
)

var (
	hookCliPath     = hookCli
	hookDefaultFile = hookDefaultFilePath
	dockerRuncName  = dockerRuncFile
	runcName        = runcFile
	deviceIdList    []int
)

const (
	// Atlas200ISoc Product name
	Atlas200ISoc = "Atlas 200I SoC A1"
	// Atlas200 Product name
	Atlas200 = "Atlas 200 Model 3000"
	// Ascend310 ascend 310 chip
	Ascend310 = "Ascend310"
	// Ascend310P ascend 310P chip
	Ascend310P = "Ascend310P"
	// Ascend310B ascend 310B chip
	Ascend310B = "Ascend310B"
	// Ascend910 ascend 910 chip
	Ascend910 = "Ascend910"
	ascend    = "Ascend"

	devicePath           = "/dev/"
	davinciName          = "davinci"
	virtualDavinciName   = "vdavinci"
	davinciManager       = "davinci_manager"
	davinciManagerDocker = "davinci_manager_docker"
	notRenameDeviceType  = ""
	devmmSvm             = "devmm_svm"
	hisiHdc              = "hisi_hdc"
	svm0                 = "svm0"
	tsAisle              = "ts_aisle"
	upgrade              = "upgrade"
	sys                  = "sys"
	vdec                 = "vdec"
	vpc                  = "vpc"
	pngd                 = "pngd"
	venc                 = "venc"
	dvppCmdList          = "dvpp_cmdlist"
	logDrv               = "log_drv"
	acodec               = "acodec"
	ai                   = "ai"
	ao                   = "ao"
	vo                   = "vo"
	hdmi                 = "hdmi"
)

type args struct {
	bundleDirPath string
	cmd           string
}

// GetDeviceTypeByChipName get device type by chipName
func GetDeviceTypeByChipName(chipName string) string {
	if strings.Contains(chipName, "310B") {
		return Ascend310B
	}
	if strings.Contains(chipName, "310P") {
		return Ascend310P
	}
	if strings.Contains(chipName, "310") {
		return Ascend310
	}
	if strings.Contains(chipName, "910") {
		return Ascend910
	}
	return ""
}

func getArgs() (*args, error) {
	args := &args{}

	for i, param := range os.Args {
		if param == "--bundle" || param == "-b" {
			if len(os.Args)-i <= 1 {
				return nil, fmt.Errorf("bundle option needs an argument")
			}
			args.bundleDirPath = os.Args[i+1]
		} else if param == "create" {
			args.cmd = param
		}
	}

	return args, nil
}

func initLogModule(ctx context.Context) error {
	const backups = 2
	const logMaxAge = 365
	runLogConfig := hwlog.LogConfig{
		LogFileName: runLogPath,
		LogLevel:    0,
		MaxBackups:  backups,
		MaxAge:      logMaxAge,
		OnlyToFile:  true,
		FileMaxSize: 2,
	}
	if err := hwlog.InitRunLogger(&runLogConfig, ctx); err != nil {
		fmt.Printf("hwlog init failed, error is %v", err)
		return err
	}
	return nil
}

var execRunc = func() error {
	tempRuncPath, err := exec.LookPath(dockerRuncName)
	if err != nil {
		tempRuncPath, err = exec.LookPath(runcName)
		if err != nil {
			return fmt.Errorf("failed to find the path of runc: %v", err)
		}
	}
	runcPath, err := filepath.EvalSymlinks(tempRuncPath)
	if err != nil {
		return fmt.Errorf("failed to find realpath of runc %v", err)
	}
	if _, err := mindxcheckutils.RealFileChecker(runcPath, true, false, mindxcheckutils.DefaultSize); err != nil {
		return err
	}

	if err := mindxcheckutils.ChangeRuntimeLogMode("runtime-run-"); err != nil {
		return err
	}
	if err = syscall.Exec(runcPath, append([]string{runcPath}, os.Args[1:]...), os.Environ()); err != nil {
		return fmt.Errorf("failed to exec runc: %v", err)
	}

	return nil
}

func addEnvToDevicePlugin(spec *specs.Spec) {
	if spec.Process.Env == nil {
		return
	}

	for _, line := range spec.Process.Env {
		words := strings.Split(line, "=")
		if len(words) == envLength && strings.TrimSpace(words[0]) == "HOSTNAME" &&
			strings.Contains(words[1], devicePlugin) {
			spec.Process.Env = append(spec.Process.Env, useAscendDocker)
			break
		}
	}
}

func addHook(spec *specs.Spec) error {
	currentExecPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot get the path of ascend-docker-runtime: %v", err)
	}

	hookCliPath = path.Join(path.Dir(currentExecPath), hookCli)
	if _, err := mindxcheckutils.RealFileChecker(hookCliPath, true, false, mindxcheckutils.DefaultSize); err != nil {
		return err
	}
	if _, err = os.Stat(hookCliPath); err != nil {
		return fmt.Errorf("cannot find ascend-docker-hook executable file at %s: %v", hookCliPath, err)
	}

	if spec.Hooks == nil {
		spec.Hooks = &specs.Hooks{}
	}

	needUpdate := true
	if len(spec.Hooks.Prestart) > maxCommandLength {
		return fmt.Errorf("too many items in Prestart ")
	}
	for _, hook := range spec.Hooks.Prestart {
		if strings.Contains(hook.Path, hookCli) {
			needUpdate = false
			break
		}
	}
	if needUpdate {
		spec.Hooks.Prestart = append(spec.Hooks.Prestart, specs.Hook{
			Path: hookCliPath,
			Args: []string{hookCliPath},
		})
	}

	if len(spec.Process.Env) > maxCommandLength {
		return fmt.Errorf("too many items in Env ")
	}

	if strings.Contains(getValueByKey(spec.Process.Env, ascendRuntimeOptions), "VIRTUAL") {
		return nil
	}

	vdevice, err := dcmi.CreateVDevice(&dcmi.NpuWorker{}, spec, deviceIdList)
	if err != nil {
		return err
	}
	hwlog.RunLog.Infof("vnpu split done: vdevice: %v", vdevice.VdeviceID)

	if vdevice.VdeviceID != -1 {
		updateEnvAndPostHook(spec, vdevice)
	}

	return nil
}

func removeDuplication(devices []int) []int {
	list := make([]int, 0, len(devices))
	prev := -1

	for _, device := range devices {
		if device == prev {
			continue
		}

		list = append(list, device)
		prev = device
	}

	return list
}

func parseDevices(visibleDevices string) ([]int, error) {
	devices := make([]int, 0)
	const maxDevice = 128

	for _, d := range strings.Split(visibleDevices, ",") {
		d = strings.TrimSpace(d)
		if strings.Contains(d, "-") {
			borders := strings.Split(d, "-")
			if len(borders) != borderNum {
				return nil, fmt.Errorf("invalid device range: %s", d)
			}

			borders[0] = strings.TrimSpace(borders[0])
			borders[1] = strings.TrimSpace(borders[1])

			left, err := strconv.Atoi(borders[0])
			if err != nil || left < 0 {
				return nil, fmt.Errorf("invalid left boarder range parameter: %s", borders[0])
			}

			right, err := strconv.Atoi(borders[1])
			if err != nil || right > maxDevice {
				return nil, fmt.Errorf("invalid right boarder range parameter: %s", borders[1])
			}

			if left > right {
				return nil, fmt.Errorf("left boarder (%d) should not be larger than the right one(%d)", left, right)
			}

			for n := left; n <= right; n++ {
				devices = append(devices, n)
			}
		} else {
			n, err := strconv.Atoi(d)
			if err != nil {
				return nil, fmt.Errorf("invalid single device parameter: %s", d)
			}

			devices = append(devices, n)
		}
	}

	sort.Slice(devices, func(i, j int) bool { return i < j })
	return removeDuplication(devices), nil
}

func parseAscendDevices(visibleDevices string) ([]int, error) {
	devicesList := strings.Split(visibleDevices, ",")
	devices := make([]int, 0, len(devicesList))
	chipType := ""

	for _, d := range devicesList {
		matchGroups := regexp.MustCompile(`^Ascend(910|310|310B|310P)-(\d+)$`).FindStringSubmatch(strings.TrimSpace(d))
		if matchGroups == nil {
			return nil, fmt.Errorf("invalid device format: %s", d)
		}
		n, err := strconv.Atoi(matchGroups[2])
		if err != nil {
			return nil, fmt.Errorf("invalid device id: %s", d)
		}

		if chipType == "" {
			chipType = matchGroups[1]
		}
		if chipType != "" && chipType != matchGroups[1] {
			return nil, fmt.Errorf("invalid device chip type: %s", d)
		}

		devices = append(devices, n)

	}
	chipName, err := dcmi.GetChipName()
	if err != nil {
		return nil, fmt.Errorf("get chip name error: %v", err)
	}
	if ascend+chipType != GetDeviceTypeByChipName(chipName) {
		return nil, fmt.Errorf("chip type not match really: %s", chipType)
	}

	sort.Slice(devices, func(i, j int) bool { return i < j })
	return removeDuplication(devices), nil
}

func getValueByKey(data []string, name string) string {
	for _, envLine := range data {
		words := strings.SplitN(envLine, "=", kvPairSize)
		if len(words) != kvPairSize {
			hwlog.RunLog.Error("environment error")
			return ""
		}

		if words[0] == name {
			return words[1]
		}
	}

	return ""
}

func getValueByDeviceKey(data []string) string {
	res := ""
	isKeyExist := false
	for _, envLine := range data {
		words := strings.SplitN(envLine, "=", kvPairSize)
		if len(words) != kvPairSize {
			hwlog.RunLog.Error("environment error")
			return ""
		}

		if words[0] == ascendVisibleDevices {
			res = words[1]
			if strings.Contains(res, ascend) {
				return res
			}
			isKeyExist = true
		}
	}
	if isKeyExist && res == "" {
		hwlog.RunLog.Error("ASCEND_VISIBLE_DEVICES env variable is empty, will not mount any ascend device")
	}

	return res
}

func addDeviceToSpec(spec *specs.Spec, dPath string, deviceType string) error {
	device, err := oci.DeviceFromPath(dPath)
	if err != nil {
		return fmt.Errorf("failed to get %s info : %#v", dPath, err)
	}

	switch deviceType {
	case virtualDavinciName:
		vDeviceNumber := regexp.MustCompile("[0-9]+").FindAllString(dPath, -1)
		if len(vDeviceNumber) != 1 {
			return fmt.Errorf("invalid vdavinci path: %s", dPath)
		}
		device.Path = devicePath + davinciName + vDeviceNumber[0]
	case davinciManagerDocker:
		device.Path = devicePath + davinciManager
	default:
		// do nothing
	}

	spec.Linux.Devices = append(spec.Linux.Devices, *device)
	newDeviceCgroup := specs.LinuxDeviceCgroup{
		Allow:  true,
		Type:   device.Type,
		Major:  &device.Major,
		Minor:  &device.Minor,
		Access: "rwm",
	}
	spec.Linux.Resources.Devices = append(spec.Linux.Resources.Devices, newDeviceCgroup)
	return nil
}

func addAscend310BManagerDevice(spec *specs.Spec) error {
	var Ascend310BManageDevices = []string{
		svm0,
		tsAisle,
		upgrade,
		sys,
		vdec,
		vpc,
		pngd,
		venc,
		dvppCmdList,
		logDrv,
		acodec,
		ai,
		ao,
		vo,
		hdmi,
	}

	for _, device := range Ascend310BManageDevices {
		dPath := devicePath + device
		if err := addDeviceToSpec(spec, dPath, notRenameDeviceType); err != nil {
			hwlog.RunLog.Warnf("failed to add %s to spec : %#v", dPath, err)
		}
	}

	davinciManagerPath := devicePath + davinciManagerDocker
	if _, err := os.Stat(davinciManagerPath); err != nil {
		hwlog.RunLog.Warnf("failed to get davinci manager docker, err: %#v", err)
		davinciManagerPath = devicePath + davinciManager
		if _, err := os.Stat(davinciManagerPath); err != nil {
			return fmt.Errorf("failed to get davinci manager, err: %#v", err)
		}
	}
	return addDeviceToSpec(spec, davinciManagerPath, davinciManagerDocker)
}

func addCommonManagerDevice(spec *specs.Spec) error {
	var commonManagerDevices = []string{
		devmmSvm,
		hisiHdc,
	}

	for _, device := range commonManagerDevices {
		dPath := devicePath + device
		if err := addDeviceToSpec(spec, dPath, notRenameDeviceType); err != nil {
			return fmt.Errorf("failed to add common manage device to spec : %#v", err)
		}
	}

	return nil
}

func addManagerDevice(spec *specs.Spec) error {
	chipName, err := dcmi.GetChipName()
	if err != nil {
		return fmt.Errorf("get chip name error: %#v", err)
	}
	devType := GetDeviceTypeByChipName(chipName)
	hwlog.RunLog.Infof("device type is: %s", devType)
	if devType == Ascend310B {
		return addAscend310BManagerDevice(spec)
	}

	if err := addDeviceToSpec(spec, devicePath+davinciManager, notRenameDeviceType); err != nil {
		return fmt.Errorf("add davinci_manager to spec error: %#v", err)
	}

	productType, err := dcmi.GetProductType(&dcmi.NpuWorker{})
	if err != nil {
		return fmt.Errorf("parse product type error: %#v", err)
	}
	hwlog.RunLog.Infof("product type is %s", productType)

	switch productType {
	// do nothing
	case Atlas200ISoc, Atlas200:
	default:
		if err = addCommonManagerDevice(spec); err != nil {
			return fmt.Errorf("add common manage device error: %#v", err)
		}
	}

	return nil
}

func checkVisibleDevice(spec *specs.Spec) ([]int, error) {
	visibleDevices := getValueByDeviceKey(spec.Process.Env)
	if visibleDevices == "" {
		return nil, nil
	}

	if strings.Contains(visibleDevices, ascend) {
		devices, err := parseAscendDevices(visibleDevices)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ascend device : %v", err)
		}
		hwlog.RunLog.Infof("ascend devices is: %v", devices)
		return devices, err
	}
	devices, err := parseDevices(visibleDevices)
	if err != nil {
		return nil, fmt.Errorf("failed to parse device : %v", err)
	}
	hwlog.RunLog.Infof("devices is: %v", devices)
	return devices, err
}

func addDevice(spec *specs.Spec) error {
	deviceName := davinciName
	if strings.Contains(getValueByKey(spec.Process.Env, ascendRuntimeOptions), "VIRTUAL") {
		deviceName = virtualDavinciName
	}
	for _, deviceId := range deviceIdList {
		dPath := devicePath + deviceName + strconv.Itoa(deviceId)
		if err := addDeviceToSpec(spec, dPath, deviceName); err != nil {
			return fmt.Errorf("failed to add davinci device to spec: %v", err)
		}
	}

	if err := addManagerDevice(spec); err != nil {
		return fmt.Errorf("failed to add Manager device to spec: %v", err)
	}

	return nil
}

func updateEnvAndPostHook(spec *specs.Spec, vdevice dcmi.VDeviceInfo) {
	newEnv := make([]string, 0, len(spec.Process.Env)+1)
	needAddVirtualFlag := true
	deviceIdList = []int{int(vdevice.VdeviceID)}
	for _, line := range spec.Process.Env {
		words := strings.Split(line, "=")
		if len(words) == envLength && strings.TrimSpace(words[0]) == ascendRuntimeOptions {
			needAddVirtualFlag = false
			if strings.Contains(words[1], "VIRTUAL") {
				newEnv = append(newEnv, line)
				continue
			} else {
				newEnv = append(newEnv, strings.TrimSpace(line)+",VIRTUAL")
				continue
			}
		}
		newEnv = append(newEnv, line)
	}
	if needAddVirtualFlag {
		newEnv = append(newEnv, fmt.Sprintf("ASCEND_RUNTIME_OPTIONS=VIRTUAL"))
	}
	spec.Process.Env = newEnv
	if currentExecPath, err := os.Executable(); err == nil {
		postHookCliPath := path.Join(path.Dir(currentExecPath), destroyHookCli)
		spec.Hooks.Poststop = append(spec.Hooks.Poststop, specs.Hook{
			Path: postHookCliPath,
			Args: []string{postHookCliPath, fmt.Sprintf("%d", vdevice.CardID), fmt.Sprintf("%d", vdevice.DeviceID),
				fmt.Sprintf("%d", vdevice.VdeviceID)},
		})
	}
}

func modifySpecFile(path string) error {
	stat, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("spec file doesnt exist %s: %v", path, err)
	}
	if _, err = mindxcheckutils.RealFileChecker(path, true, true, mindxcheckutils.DefaultSize); err != nil {
		return err
	}

	jsonFile, err := os.OpenFile(path, os.O_RDWR, stat.Mode())
	if err != nil {
		return fmt.Errorf("cannot open oci spec file %s: %v", path, err)
	}

	defer jsonFile.Close()

	jsonContent, err := ioutil.ReadAll(jsonFile)
	if err != nil {
		return fmt.Errorf("failed to read oci spec file %s: %v", path, err)
	}

	if err = jsonFile.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate: %v", err)
	}
	if _, err = jsonFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek: %v", err)
	}

	var spec specs.Spec
	if err = json.Unmarshal(jsonContent, &spec); err != nil {
		return fmt.Errorf("failed to unmarshal oci spec file %s: %v", path, err)
	}

	devices, err := checkVisibleDevice(&spec)
	if err != nil {
		hwlog.RunLog.Errorf("failed to check ASCEND_VISIBLE_DEVICES parameter, err: %v", err)
		return fmt.Errorf("failed to check ASCEND_VISIBLE_DEVICES parameter, err: %v", err)
	}
	if len(devices) != 0 {
		deviceIdList = devices
		if err = addHook(&spec); err != nil {
			hwlog.RunLog.Errorf("failed to inject hook, err: %v", err)
			return fmt.Errorf("failed to inject hook, err: %v", err)
		}
		if err = addDevice(&spec); err != nil {
			return fmt.Errorf("failed to add device to env: %v", err)
		}
		if err = addLDEnv(&spec); err != nil {
			return fmt.Errorf("failed to add LD_LIBRARY_PATH to env: %v", err)
		}
	}

	addEnvToDevicePlugin(&spec)

	jsonOutput, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal OCI spec file: %v", err)
	}

	if _, err = jsonFile.WriteAt(jsonOutput, 0); err != nil {
		return fmt.Errorf("failed to write OCI spec file: %v", err)
	}

	return nil
}

func addLDEnv(spec *specs.Spec) error {
	ldEnvKey := "LD_LIBRARY_PATH"
	ldEnvValue := "/usr/local/Ascend/driver/lib64/common:/usr/local/Ascend/driver/lib64/driver"
	for _, val := range spec.Process.Env {
		kv := strings.Split(val, "=")
		if len(kv) != envLength {
			continue
		}
		k, v := kv[0], kv[1]
		if k != ldEnvKey {
			continue
		}
		spec.Process.Env = append(spec.Process.Env, ldEnvKey+"="+v+":"+ldEnvValue)
		return nil
	}
	spec.Process.Env = append(spec.Process.Env, ldEnvKey+"="+ldEnvValue)
	return nil
}

func doProcess() error {
	args, err := getArgs()
	if err != nil {
		return fmt.Errorf("failed to get args: %v", err)
	}

	if args.cmd != "create" {
		return execRunc()
	}

	if args.bundleDirPath == "" {
		args.bundleDirPath, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working dir: %v", err)
		}
	}

	specFilePath := args.bundleDirPath + "/config.json"

	if err = modifySpecFile(specFilePath); err != nil {
		return fmt.Errorf("failed to modify spec file %s: %v", specFilePath, err)
	}

	return execRunc()
}

func main() {
	defer func() {
		if err := recover(); err != nil {
			log.Fatal(err)
		}
	}()
	ctx, _ := context.WithCancel(context.Background())
	if err := initLogModule(ctx); err != nil {
		log.Fatal(err)
	}
	logPrefixWords, err := mindxcheckutils.GetLogPrefix()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err = mindxcheckutils.ChangeRuntimeLogMode("runtime-run-"); err != nil {
			fmt.Println("defer changeFileMode function failed")
		}
	}()
	if !mindxcheckutils.StringChecker(strings.Join(os.Args, " "), 0,
		maxCommandLength, mindxcheckutils.DefaultWhiteList+" ") {
		hwlog.RunLog.Errorf("%v ascend docker runtime args check failed", logPrefixWords)
		log.Fatal("command error")
	}
	if err = doProcess(); err != nil {
		hwlog.RunLog.Errorf("%v docker runtime failed: %v", logPrefixWords, err)
		log.Fatal(err)
	}
}
