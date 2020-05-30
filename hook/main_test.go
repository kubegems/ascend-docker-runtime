/*
 * Copyright (c) Huawei Technologies Co., Ltd. 2020-2020. All rights reserved.
 * Description: hook main 函数单元测试
 */
package main

import (
	"github.com/prashantv/gostub"
	"os"
	"os/exec"
	"reflect"
	"testing"
)

func TestRemoveDuplication(t *testing.T) {
	originList := []int{1, 2, 2, 4, 5, 5, 5, 6, 8, 8}
	targetList := []int{1, 2, 4, 5, 6, 8}
	resultList := removeDuplication(originList)

	if !reflect.DeepEqual(resultList, targetList) {
		t.Fail()
	}
}

func TestDoPrestartHookCase1(t *testing.T) {
	if err := doPrestartHook(); err != nil {
		t.Log("failed")
	}
}

func TestDoPrestartHookCase2(t *testing.T) {
	conCfg := containerConfig{
		Pid:    123,
		Rootfs: ".",
		Env:    []string{"ASCEND_VISIBLE_DEVICES=0l-3,5,7"},
	}
	stub := gostub.StubFunc(&getContainerConfig, &conCfg, nil)
	defer stub.Reset()
	if err := doPrestartHook(); err != nil {
		t.Log("failed")
	}
}

func TestDoPrestartHookCase3(t *testing.T) {
	conCfg := containerConfig{
		Pid:    123,
		Rootfs: ".",
		Env:    []string{"ASCEND_VISIBLE_DEVICE=0-3,5,7"},
	}
	stub := gostub.StubFunc(&getContainerConfig, &conCfg, nil)
	defer stub.Reset()
	if err := doPrestartHook(); err != nil {
		t.Log("failed")
	}
}

func TestDoPrestartHookCase4(t *testing.T) {
	conCfg := containerConfig{
		Pid:    123,
		Rootfs: ".",
		Env:    []string{"ASCEND_VISIBLE_DEVICES=0-3,5,7"},
	}
	stub := gostub.StubFunc(&getContainerConfig, &conCfg, nil)
	defer stub.Reset()
	stub.Stub(&ascendDockerCliName, "")
	stub.StubFunc(&sysCallExec, nil)
	if err := doPrestartHook(); err != nil {
		t.Log("failed")
	}
}

func TestDoPrestartHookCase5(t *testing.T) {
	defer func() {
		if err := recover(); err != nil {
			t.Log("exception", err)
		}
	}()
	conCfg := containerConfig{
		Pid:    123,
		Rootfs: ".",
		Env:    []string{"ASCEND_VISIBLE_DEVICES=0-3,5,7"},
	}
	stub := gostub.StubFunc(&getContainerConfig, &conCfg, nil)
	defer stub.Reset()
	stub.Stub(&ascendDockerCliName, "clii")
	stub.Stub(&defaultAscendDockerCliName, "clii")
	stub.StubFunc(&sysCallExec, nil)
	if err := doPrestartHook(); err != nil {
		t.Log("failed")
	}
}

func TestGetValueByKeyCase1(t *testing.T) {
	data := []string{"ASCEND_VISIBLE_DEVICES=0-3,5,7"}
	key := "ASCEND_VISIBLE_DEVICES"
	expectVal := "0-3,5,7"
	actualVal := getValueByKey(data, key)
	if actualVal != expectVal {
		t.Fail()
	}
}

func TestGetValueByKeyCase2(t *testing.T) {
	data := []string{"ASCEND_VISIBLE_DEVICES"}
	key := "ASCEND_VISIBLE_DEVICES"
	expectVal := ""
	defer func() {
		if err := recover(); err != nil {
			t.Log("exception occur")
		}
	}()
	actualVal := getValueByKey(data, key)
	if actualVal != expectVal {
		t.Fail()
	}
}

func TestGetValueByKeyCase3(t *testing.T) {
	data := []string{"ASCEND_VISIBLE_DEVICES=0-3,5,7"}
	key := "ASCEND_VISIBLE_DEVICE"
	expectVal := ""
	actualVal := getValueByKey(data, key)
	if actualVal != expectVal {
		t.Fail()
	}
}

func TestParseDevicesCase1(t *testing.T) {
	visibleDevices := "0-3,5,7"
	expectVal := []int{0, 1, 2, 3, 5, 7}
	actualVal, err := parseDevices(visibleDevices)
	if err != nil || !reflect.DeepEqual(expectVal, actualVal) {
		t.Fail()
	}
}

func TestParseDevicesCase2(t *testing.T) {
	visibleDevices := "0-3-4,5,7"
	_, err := parseDevices(visibleDevices)
	if err != nil {
		t.Fail()
	}
}

func TestParseDevicesCase3(t *testing.T) {
	visibleDevices := "0l-3,5,7"
	_, err := parseDevices(visibleDevices)
	if err == nil {
		t.Fail()
	}
}

func TestParseDevicesCase4(t *testing.T) {
	visibleDevices := "0-3o,5,7"
	_, err := parseDevices(visibleDevices)
	if err == nil {
		t.Fail()
	}
}

func TestParseDevicesCase5(t *testing.T) {
	visibleDevices := "4-3,5,7"
	_, err := parseDevices(visibleDevices)
	if err == nil {
		t.Fail()
	}
}

func TestParseDevicesCase6(t *testing.T) {
	visibleDevices := "3o,5,7"
	_, err := parseDevices(visibleDevices)
	if err == nil {
		t.Fail()
	}
}

func TestParseDevicesCase7(t *testing.T) {
	visibleDevices := "0=3,5,7"
	_, err := parseDevices(visibleDevices)
	if err == nil {
		t.Fail()
	}
}

func TestParseOciSpecFileCase1(t *testing.T) {
	file := "file"
	_, err := parseOciSpecFile(file)
	if err == nil {
		t.Fail()
	}
}

func TestParseOciSpecFileCase2(t *testing.T) {
	file := "file"
	f, err := os.Create(file)
	defer os.Remove(file)
	defer f.Close()
	if err != nil {
		t.Log("create file failed")
	}
	_, err = parseOciSpecFile(file)
	if err == nil {
		t.Fail()
	}
}

func TestParseOciSpecFileCase3(t *testing.T) {
	file := "config.json"
	cmd := exec.Command("runc", "spec")
	if err := cmd.Run(); err != nil {
		t.Log("runc spec failed")
	}
	defer os.Remove(file)
	_, err := parseOciSpecFile(file)
	if err != nil {
		t.Fail()
	}
}

func TestGetContainerConfig(t *testing.T) {
	file := "config.json"
	cmd := exec.Command("runc", "spec")
	if err := cmd.Run(); err != nil {
		t.Log("runc spec failed")
	}
	defer func() {
		if err := recover(); err != nil {
			t.Log("exception", err)
		}
	}()
	defer os.Remove(file)
	stateFile, err := os.Open("config.json")
	if err != nil {
		t.Log("open file failed")
	}
	defer stateFile.Close()

	stub := gostub.Stub(&stdIn, stateFile)
	defer stub.Reset()

	getContainerConfig()
}
