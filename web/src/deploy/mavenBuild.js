export function validMavenThreads(value){
 const text=String(value||'').trim()
 if(!text)return true
 const number=Number(text.replace(/C$/,''))
 return text.length<=32&&/^(?:[1-9][0-9]*|[0-9]+(?:\.[0-9]+)?C)$/.test(text)&&Number.isFinite(number)&&number>0
}

export function mavenLockFailure(log){
 if(!/Could not acquire lock\(s\)|Could not acquire (?:exclusive|shared|read|write) lock/.test(String(log||'')))return null
 return {title:'Maven 依赖仓库锁获取失败',repair:'maven-arguments',detail:'依赖解析获取锁失败，当前日志未必包含持锁原因。先将并行线程设为 1，在命令行附加参数填写 -e，保存后按当前配置新建构建；核对异常堆栈中的依赖、仓库响应和锁等待。原快照重试不会使用新配置。'}
}
