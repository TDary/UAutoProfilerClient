package ParseServer

import (
	"MasterClient/Logs"
	"MasterClient/Minio"
	"MasterClient/RabbitMqServer"
	"MasterClient/UnityServer"
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//获取解析成功的数据
func AnalyzeSuccess(data string) {
	ParseSuccessData(data)
}

//获取解析器状态请求
func RequestMachineState(data string) {
	Logs.Loggers().Print(data)
}

//从http消息中获取任务并存入队列中
func GetAnalyzeMes(data string) {
	filepath := "./AnalyTask"
	_, err := os.Stat(filepath)
	if err != nil {
		os.Mkdir(filepath, 0755)
	}
	RabbitMqServer.PutData(filepath+"/ParseQue", data)
}

//检测队列中的解析任务并拿出来进行解析
func AnalyzeRangeCheck() {
	taskPath := "./AnalyTask/ParseQue"
	for {
		//有空闲的才去分配解析
		_, err := os.Stat(taskPath)
		if err == nil {
			if UnityServer.CheckFreeAnalyze() {
				taskdata := RabbitMqServer.GetData(taskPath)
				if taskdata != "" {
					go Analyze(taskdata)
				}
			}
		} else {
			//Logs.Loggers().Print("无待解析的队列文件----")
		}
		time.Sleep(3 * time.Second) //每隔3秒开启下一个解析任务
	}
}

// 开始启动解析进程
func Analyze(data string) {
	Logs.Loggers().Print("开始解析任务----")
	var getdata UnityServer.AnalyzeData
	project, num := UnityServer.GetUnityProject()
	getdata = ParseData(data, getdata) //解析传入的数据
	successDownLoad := UnityServer.DownLoadRawFile(getdata)
	if successDownLoad {
		if getdata.AnalyzeType == "simple" {
			projectID := UnityServer.StartAnalyzeForCsvProfiler(getdata, project, num)
			CheckProcessState(getdata, projectID) //监控解析进程
			Logs.Loggers().Print("解析流程完毕----")
		} else if getdata.AnalyzeType == "funprofiler" {
			projectID := UnityServer.StartAnalyzeForFunProfiler(getdata, project, num)
			CheckProcessState(getdata, projectID) //监控解析进程
			Logs.Loggers().Print("解析流程完毕----")
		} else {
			//deep  暂时不解析
		}
	} else {
		Logs.Loggers().Print("下载源文件" + getdata.RawFile + "失败----")
	}
}

//循环检测进程
func CheckProcessState(getdata UnityServer.AnalyzeData, id int) {
	getdata.AnalyzeNum = id //把工程id赋值
	var count int
	for {
		time.Sleep(5 * time.Second)
		state := CheckAnalyzeState(getdata)
		if state == "success" {
			Logs.Loggers().Print("UUID:" + getdata.UUID + ",rawFile:" + getdata.RawFile + "解析成功----")
			UnityServer.RleaseUnityProject(id)
			UnityServer.SuccessAnalyze(getdata)
			UploadSuccessedData(getdata)
			//完成解析消息回传发送
			UnityServer.GetSucessData(getdata.RawFile, getdata.UUID)
			break
		} else if state == "failed" {
			Logs.Loggers().Print("UUID:" + getdata.UUID + ",rawFile:" + getdata.RawFile + "解析失败----")
			UnityServer.RleaseUnityProject(id)
			UnityServer.SuccessAnalyze(getdata)
			UploadSuccessedData(getdata)
			//解析失败消息上报
			UnityServer.SendFailMessage(getdata.RawFile, getdata.UUID)
			break
		} else {
			//超过一定的等待时间即代表着已经解析出问题了
			if count >= 24 {
				//释放unity解析池组
				UnityServer.RleaseUnityProject(id)
				UnityServer.SuccessAnalyze(getdata)
				//解析失败消息上报
				UnityServer.SendFailMessage(getdata.RawFile, getdata.UUID)
				break
			}
		}
		count++
	}
}

//检查解析完毕的数组是否有对应的
func CheckAnalyzeState(getdata UnityServer.AnalyzeData) string {
	logicMutex.TryLock()
	for i := 0; i < len(analyzeData); i++ {
		if analyzeData[i].RawFile == getdata.RawFile && analyzeData[i].UUID == getdata.UUID &&
			analyzeData[i].AnalyzeType == getdata.AnalyzeType && analyzeData[i].State == "success" {
			analyzeData = append(analyzeData[:i], analyzeData[i+1:]...)
			logicMutex.Unlock()
			return "success"
		} else if analyzeData[i].RawFile == getdata.RawFile && analyzeData[i].UUID == getdata.UUID &&
			analyzeData[i].AnalyzeType == getdata.AnalyzeType && analyzeData[i].State == "failed" {
			analyzeData = append(analyzeData[:i], analyzeData[i+1:]...)
			logicMutex.Unlock()
			return "failed"
		}
	}
	logicMutex.Unlock()
	return "wait"
}

// 将回传的http消息进行处理
func ParseData(data string, gdata UnityServer.AnalyzeData) UnityServer.AnalyzeData {
	current := strings.Split(data, "&")
	for i := 0; i < len(current); i++ {
		if strings.Contains(current[i], "uuid") {
			cdata := strings.Split(current[i], "=")
			gdata.UUID = cdata[1]
		} else if strings.Contains(current[i], "rawfile") {
			cdata := strings.Split(current[i], "=")
			gdata.RawFile = cdata[1]
		} else if strings.Contains(current[i], "unityversion") {
			cdata := strings.Split(current[i], "=")
			gdata.UnityVersion = cdata[1]
		} else if strings.Contains(current[i], "analyzebucket") {
			cdata := strings.Split(current[i], "=")
			gdata.AnalyzeBucket = cdata[1]
		} else if strings.Contains(current[i], "analyzeType") {
			cdata := strings.Split(current[i], "=")
			gdata.AnalyzeType = cdata[1]
		}
	}
	return gdata
}

//将回传的成功http消息进行处理
func ParseSuccessData(data string) {
	var gdata UnityServer.AnalyzeState
	current := strings.Split(data, "&")
	for i := 0; i < len(current); i++ {
		if strings.Contains(current[i], "uuid") {
			cdata := strings.Split(current[i], "=")
			gdata.UUID = cdata[1]
		} else if strings.Contains(current[i], "rawfile") {
			cdata := strings.Split(current[i], "=")
			gdata.RawFile = cdata[1]
		} else if strings.Contains(current[i], "anaType") {
			cdata := strings.Split(current[i], "=")
			gdata.AnalyzeType = cdata[1]
		} else if strings.Contains(current[i], "state") {
			cdata := strings.Split(current[i], "=")
			gdata.State = cdata[1]
		}
	}
	analyzeData = append(analyzeData, gdata)
}

//上传解析完成的文件
func UploadSuccessedData(getdata UnityServer.AnalyzeData) {
	uploadMutex.TryLock()
	var currentAnalyzePath strings.Builder
	var objectName strings.Builder
	currentAnalyzePath.WriteString(UnityServer.GetConfig().FilePath)
	currentAnalyzePath.WriteString("/")
	currentAnalyzePath.WriteString(getdata.UUID)
	currentAnalyzePath.WriteString("/")
	currentAnalyzePath.WriteString(strings.Split(getdata.RawFile, ".")[0])
	//压缩
	destinaFile := currentAnalyzePath.String() + ".zip"
	err := CompressFolder(currentAnalyzePath.String(), destinaFile)
	if err != nil {
		Logs.Loggers().Print("Compress result file failed----")
		return
	}
	objectName.WriteString(getdata.UUID)
	objectName.WriteString("/")
	objectName.WriteString(strings.Split(getdata.RawFile, ".")[0])
	objectName.WriteString(".zip")
	contentType := "application/zip"
	Minio.UploadFile(objectName.String(), destinaFile, contentType)
	uploadMutex.Unlock()
}

//压缩文件夹
func CompressFolder(sourceFolder, targetFile string) error {
	zipFile, err := os.Create(targetFile)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	err = filepath.Walk(sourceFolder, func(filePath string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !fileInfo.IsDir() {
			file, err := os.Open(filePath)
			if err != nil {
				return err
			}
			defer file.Close()

			relPath, err := filepath.Rel(sourceFolder, filePath)
			if err != nil {
				return err
			}

			zipEntry, err := zipWriter.Create(relPath)
			if err != nil {
				return err
			}

			_, err = io.Copy(zipEntry, file)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}
