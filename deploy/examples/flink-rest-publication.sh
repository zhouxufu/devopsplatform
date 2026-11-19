#!/usr/bin/env bash
set -Eeuo pipefail
if [[ -n "${FLINK_TOOLS_DIR:-}" ]]; then
    [[ "$FLINK_TOOLS_DIR" = /* && -d "$FLINK_TOOLS_DIR" ]] || { echo 'Flink tools directory unavailable' >&2; exit 2; }
    export PATH="$FLINK_TOOLS_DIR:$PATH"
fi
# Example for the generic script publication target. No deployment happens on save.
# Arguments: job key, optional savepoint. Variables belong to the application environment.
: "${LP_ARTIFACT_PATH:?missing reviewed artifact}"
: "${LP_ARTIFACT_DIGEST:?missing artifact digest}"
: "${DELIVERY_DEPLOY_ID:?missing deployment identity}"
: "${LP_RESULT_FILE:?missing result file}"
[[ $# -ge 1 && $# -le 2 ]] || { echo 'Expected job key and optional savepoint' >&2; exit 2; }
set -- "$1" "$LP_ARTIFACT_PATH" "${2:-}"
write_result() {
  jq -n --arg status "$1" --arg phase "$2" --arg external_id "${job_id:-}" \
    --arg old_job_id "${old_job_id:-}" --arg jar_id "${jar_id:-}" \
    --arg savepoint "${savepoint:-}" --arg request_id "${stop_request_id:-}" \
    '{status:$status,external_id:$external_id,values:{phase:$phase,old_job_id:$old_job_id,jar_id:$jar_id,savepoint:$savepoint,request_id:$request_id}}' > "$LP_RESULT_FILE.new"
  mv -f -- "$LP_RESULT_FILE.new" "$LP_RESULT_FILE"
}
# 通过 Flink REST 幂等发布一个测试环境作业；同名作业存在时从 Savepoint 滚动替换。
if [[ $# -lt 2 || $# -gt 3 ]]; then
    echo "用法：$0 <behavior|user-demand-cdc|house-favorite-cdc|house-compare-cdc|house-offline-view> <job.jar> [savepoint]" >&2
    exit 2
fi

job="$1"
package="$2"
savepoint="${3:-}"
flink_rest_url="${FLINK_REST_URL:?configure FLINK_REST_URL}"
savepoint_root="${FLINK_SAVEPOINT_DIR:?configure FLINK_SAVEPOINT_DIR}"
poll_interval_seconds=2
operation_timeout_seconds=300

[[ -f "$package" ]] || { echo "作业包不存在：$package" >&2; exit 2; }
command -v curl >/dev/null 2>&1 || { echo "需要 curl" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "需要 jq" >&2; exit 2; }
if command -v sha256sum >/dev/null 2>&1; then
    hash_command=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
    hash_command=(shasum -a 256)
else
    echo '需要 sha256sum 或 shasum；尚未上传或停止作业' >&2
    exit 2
fi

case "$job" in
behavior)
    main="com.hz.zfb.intelligence.flink.behavior.HouseViewBehaviorJob"
    job_name="zfb-house-view-requirement-pipeline"
    ;;
user-demand-cdc)
    main="com.hz.zfb.intelligence.flink.cdc.UserDemandConfigCdcJob"
    job_name="zfb-user-demand-config-cdc"
    ;;
house-favorite-cdc)
    main="com.hz.zfb.intelligence.flink.cdc.HouseFavoriteCdcJob"
    job_name="zfb-house-favorite-cdc"
    ;;
house-compare-cdc)
    main="com.hz.zfb.intelligence.flink.cdc.HouseCompareCdcJob"
    job_name="zfb-house-compare-cdc"
    ;;
house-offline-view)
    main="com.hz.zfb.intelligence.flink.cdc.HouseOfflineViewCdcJob"
    job_name="zfb-house-offline-view-cdc"
    ;;
*) echo "未知作业：$job" >&2; exit 2;;
esac

# 在上传、停止旧任务之前核对引擎，禁止把 Java11/Flink1.20 作业误发到旧2.x集群。
cluster_config="$(curl --silent --show-error --fail --max-time 30 "$flink_rest_url/config")"
cluster_version="$(jq --raw-output '."flink-version" // empty' <<<"$cluster_config")"
if [[ "$cluster_version" != 1.20.* ]]; then
    echo "测试集群必须先切换到 Flink1.20，当前版本：${cluster_version:-未知}" >&2
    exit 1
fi

write_result pending preflight

# 查询指定名称的非终态实例；同一作业出现多个实例时拒绝继续，避免停止错误任务。
active_jobs="$(curl --silent --show-error --fail --max-time 30 \
    "$flink_rest_url/jobs/overview" \
    | jq --raw-output --arg name "$job_name" '
        .jobs[]
        | select(.name == $name)
        | select(.state != "FINISHED" and .state != "CANCELED"
            and .state != "FAILED" and .state != "SUSPENDED")
        | [.jid, .state]
        | @tsv')"
active_job_count="$(awk 'NF { count++ } END { print count + 0 }' <<<"$active_jobs")"
if (( active_job_count > 1 )); then
    echo "检测到多个同名运行实例，拒绝自动替换：name=$job_name" >&2
    printf '%s\n' "$active_jobs" >&2
    exit 1
fi

if (( active_job_count == 1 )); then
    if [[ -n "$savepoint" ]]; then
        echo "同名作业正在运行时不能手工指定 Savepoint：name=$job_name" >&2
        exit 2
    fi

    IFS=$'\t' read -r old_job_id old_job_state <<<"$active_jobs"
    if [[ "$old_job_state" != "RUNNING" ]]; then
        echo "同名作业当前不是 RUNNING，拒绝自动替换：jobId=${old_job_id}，state=${old_job_state}" >&2
        exit 1
    fi
fi

# 先上传新包，确保停止旧作业前待发布产物已经可供 Flink 使用。
upload_response="$(curl --silent --show-error --fail --max-time 30 \
    -X POST "$flink_rest_url/jars/upload" \
    -F "jarfile=@$package")"
uploaded_filename="$(jq --raw-output '.filename // empty' <<<"$upload_response")"
jar_id="${uploaded_filename##*/}"
[[ -n "$jar_id" ]] || {
    echo "Flink 上传响应缺少 filename：$upload_response" >&2
    exit 1
}

write_result pending uploaded

if (( active_job_count == 1 )); then
    echo "检测到同名作业，开始生成 Savepoint 并停止旧实例：name=${job_name}，jobId=${old_job_id}"
    stop_payload="$(jq -n \
        --arg target_directory "$savepoint_root/$job" \
        '{drain: false, formatType: "CANONICAL", targetDirectory: $target_directory}')"
    stop_response="$(curl --silent --show-error --fail --max-time 30 \
        -X POST "$flink_rest_url/jobs/$old_job_id/stop" \
        -H 'Content-Type: application/json' \
        --data "$stop_payload")"
    stop_request_id="$(jq --raw-output '."request-id" // empty' <<<"$stop_response")"
    [[ -n "$stop_request_id" ]] || {
        echo "Flink 停止响应缺少 request-id：$stop_response" >&2
        exit 1
    }

    write_result pending stopping
    deadline=$((SECONDS + operation_timeout_seconds))
    while (( SECONDS < deadline )); do
        savepoint_response="$(curl --silent --show-error --fail --max-time 30 \
            "$flink_rest_url/jobs/$old_job_id/savepoints/$stop_request_id")"
        savepoint_status="$(jq --raw-output '.status.id // empty' <<<"$savepoint_response")"
        if [[ "$savepoint_status" == "COMPLETED" ]]; then
            failure_class="$(jq --raw-output '.operation["failure-cause"].class // empty' <<<"$savepoint_response")"
            if [[ -n "$failure_class" ]]; then
                echo "旧作业停止失败：$savepoint_response" >&2
                exit 1
            fi
            savepoint="$(jq --raw-output '.operation.location // empty' <<<"$savepoint_response")"
            [[ -n "$savepoint" ]] || {
                write_result pending savepoint-ready
            echo "旧作业已停止，但响应缺少 Savepoint 路径：$savepoint_response" >&2
                exit 1
            }
            echo "旧作业已停止，Savepoint：$savepoint"
            break
        fi
        [[ "$savepoint_status" == "IN_PROGRESS" ]] || {
            echo "无法识别 Savepoint 状态：$savepoint_response" >&2
            exit 1
        }
        sleep "$poll_interval_seconds"
    done
    [[ -n "$savepoint" ]] || {
        echo "等待旧作业停止超时：jobId=$old_job_id" >&2
        exit 1
    }
fi

# Persist the intended ID before submission so a lost HTTP response can be reconciled.
job_id="$(printf '%s' "$DELIVERY_DEPLOY_ID:$LP_SERVICE:$LP_ARTIFACT_DIGEST" | "${hash_command[@]}" | cut -c1-32)"
write_result pending submitting
run_payload="$(jq -n \
    --arg job_id "$job_id" \
    --arg entry_class "$main" \
    --arg savepoint_path "$savepoint" \
    '({"entryClass": $entry_class, "jobId": $job_id} + (if $savepoint_path == "" then {} else {"savepointPath": $savepoint_path} end))')"
run_response="$(curl --silent --show-error --fail --max-time 30 \
    -X POST "$flink_rest_url/jars/$jar_id/run" \
    -H 'Content-Type: application/json' \
    --data "$run_payload")"

returned_job_id="$(jq --raw-output '.jobid // empty' <<<"$run_response")"
[[ "$returned_job_id" == "$job_id" ]] || { echo 'Submission identity differs; reconcile before retry' >&2; exit 1; }
write_result pending verifying
if [[ -z "$job_id" ]]; then
    echo "Flink 启动响应缺少 jobid：$run_response" >&2
    exit 1
fi

deadline=$((SECONDS + operation_timeout_seconds))
while (( SECONDS < deadline )); do
    job_response="$(curl --silent --show-error --fail --max-time 30 \
        "$flink_rest_url/jobs/$job_id")"
    job_state="$(jq --raw-output '.state // empty' <<<"$job_response")"
    case "$job_state" in
    RUNNING)
        vertices_ready="$(jq --raw-output '[.vertices[].status] | length > 0 and all(. == "RUNNING")' <<<"$job_response")"
        if [[ "$vertices_ready" == "true" ]]; then
            write_result success running
            echo "测试 Flink Job 发布成功：name=${job_name}，jobId=${job_id}，jar=${jar_id}"
            exit 0
        fi
        ;;
    FINISHED|CANCELED|FAILED|SUSPENDED)
        echo "新作业未进入 RUNNING：jobId=${job_id}，state=${job_state}" >&2
        exit 1
        ;;
    esac
    sleep "$poll_interval_seconds"
done

echo "等待新作业进入 RUNNING 超时：jobId=$job_id" >&2
exit 1
