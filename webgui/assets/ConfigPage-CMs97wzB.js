import{Ar as e,Hr as t,Mr as n,Yr as r,cr as i,dr as a,hi as o,hr as s,i as c,kr as l,lr as u,mr as d,o as f,r as p,t as m,ur as h}from"./api-Dzra4sQq.js";import{n as g,t as _}from"./css-B7DnZOxI.js";import{_ as v,u as y}from"./index-DlTylvU5.js";var b={class:`page-heading`},x={class:`panel`},S={class:`panel-heading`},C={class:`config-note`},w={__name:`ConfigPage`,setup(w){let T=r(null),E=r(``),D=r(!1),O=new AbortController;l(async()=>{try{T.value=await m(`/config`,{signal:O.signal})}catch(e){O.signal.aborted||(E.value=e.message)}}),e(()=>O.abort());async function k(){try{await y.confirm(`请先保存 conf 文件。仅新增、移除或配置有变化的程序会被更新，连接将自动重建；需要重新登录。`,`重新加载配置`,{type:`warning`,confirmButtonText:`加载配置`,cancelButtonText:`取消`})}catch{return}D.value=!0;try{await m(`/config/reload`,{method:`POST`}),p.success(`已接受配置重载请求`)}catch(e){p.error(e.message)}finally{D.value=!1}}return(e,r)=>{let l=f,p=v,m=c,y=g,w=_;return n(),a(`section`,null,[i(`div`,b,[r[1]||=i(`div`,null,[i(`span`,{class:`eyebrow`},`CONFIGURATION`),i(`h1`,null,`配置管理`),i(`p`,null,`沿用熟悉的 conf 文件，明确控制每个程序的行为。`)],-1),s(l,{type:`primary`,loading:D.value,onClick:k},{default:t(()=>[...r[0]||=[d(`加载配置变更`,-1)]]),_:1},8,[`loading`])]),E.value?(n(),u(p,{key:0,title:E.value,type:`error`,closable:!1},null,8,[`title`])):h(``,!0),i(`article`,x,[i(`div`,S,[r[3]||=i(`h2`,null,`当前配置`,-1),s(m,{type:`info`},{default:t(()=>[...r[2]||=[d(`只读预览`,-1)]]),_:1})]),i(`div`,C,[r[4]||=i(`span`,{class:`subtle`},`配置文件路径`,-1),i(`code`,null,o(T.value?.file||`加载中…`),1),r[5]||=i(`p`,null,`在服务器编辑配置后，点击“加载配置变更”。账号、私钥和环境变量不会在此页面展示。`,-1)]),s(w,{data:T.value?.programs||[],"empty-text":`尚未配置程序`},{default:t(()=>[s(y,{prop:`name`,label:`程序`,"min-width":`160`}),s(y,{prop:`command`,label:`启动命令`,"min-width":`260`,"show-overflow-tooltip":``}),s(y,{prop:`directory`,label:`工作目录`,"min-width":`220`,"show-overflow-tooltip":``}),s(y,{prop:`autorestart`,label:`自动重启`,width:`140`}),s(y,{label:`停止信号`,width:`120`,prop:`stopsignal`}),s(y,{label:`退出等待`,width:`110`},{default:t(({row:e})=>[d(o(e.stopwaitsecs)+` 秒`,1)]),_:1})]),_:1},8,[`data`])]),r[6]||=i(`article`,{class:`panel config-example`},[i(`div`,{class:`panel-heading`},[i(`h2`,null,`程序配置示例`)]),i(`pre`,null,`[program:my-app]
directory = %(here)s/apps/my-app
command = ./my-app --name "hello world" --label 'single quotes'
autostart = true
autorestart = unexpected
stdout_logfile = logs/stdout.log
stderr_logfile = logs/stderr.log
stdout_logfile_timestamp_suffix = false
stderr_logfile_timestamp_suffix = false
stdout_logfile_maxbytes = 50MB
stderr_logfile_maxbytes = 50MB
stdout_logfile_backups = 5
stderr_logfile_backups = 5
stopsignal = TERM
stopwaitsecs = 10
stopasgroup = true
killasgroup = true`),i(`div`,{class:`table-note`},` 程序工作目录相对于 conf 所在目录解析；相对日志路径基于程序工作目录。需要 Shell 功能时显式使用 /bin/sh -c。 `)],-1)])}}};export{w as default};