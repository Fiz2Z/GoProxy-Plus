package webui

const loginHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>GoProxy Plus — 身份验证</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:linear-gradient(180deg,#f8fafc,#edf1f6);color:#172033;font-family:Inter,-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;padding:24px}
.card{border:1px solid #e1e6ee;padding:42px;width:min(420px,100%);background:rgba(255,255,255,.94);border-radius:22px;box-shadow:0 24px 70px rgba(29,43,70,.12)}
h1{font-size:30px;font-weight:750;margin-bottom:6px;letter-spacing:-.035em;color:#172033}
.sub{color:#758196;font-size:12px;margin-bottom:34px;letter-spacing:.02em}
label{display:block;font-size:12px;color:#5d687a;margin-bottom:8px;font-weight:650}
input[type=password]{width:100%;padding:13px 14px;background:#fff;border:1px solid #d9e0ea;border-radius:10px;color:#172033;font-size:15px;outline:none;transition:border-color .16s ease,box-shadow .16s ease}
input[type=password]:focus{border-color:#6789d8;box-shadow:0 0 0 4px rgba(50,102,213,.12)}
button{width:100%;margin-top:18px;padding:13px;background:#3266d5;color:#fff;border:0;border-radius:10px;font-size:13px;font-weight:700;cursor:pointer;transition:background .16s ease,transform .1s ease}
button:hover{background:#2859bf}button:active{transform:scale(.98)}
.logo{display:inline-flex;align-items:center;justify-content:center;width:44px;height:44px;margin-bottom:22px;border-radius:13px;background:#edf3ff;color:#3266d5;font-size:14px;font-weight:800;letter-spacing:-.02em}
.tip{color:#758196;font-size:11px;margin-top:22px;line-height:1.6;text-align:center}.tip a{color:#3266d5;text-decoration:none}.tip a:hover{text-decoration:underline}
.github{position:absolute;top:22px;right:22px;color:#758196;transition:color .16s ease}.github:hover{color:#172033}
@media(prefers-reduced-motion:reduce){*{transition-duration:.01ms!important}}
</style>
</head>
<body>
<a href="https://github.com/Fiz2Z/GoProxy-Plus" target="_blank" class="github" title="GitHub">
  <svg width="32" height="32" viewBox="0 0 16 16" fill="currentColor">
    <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/>
  </svg>
</a>
<div class="card">
  <div class="logo">GP+</div>
  <h1>GoProxy Plus</h1>
  <p class="sub">智能代理池管理</p>
  <form method="POST" action="/login">
    <label>管理密码</label>
    <input type="password" name="password" placeholder="****************" autofocus>
    <button type="submit">登录控制台</button>
  </form>
  <p class="tip">访客模式可<a href="/">查看数据</a>，管理员登录后可完全控制</p>
</div>
</body>
</html>`

const loginHTMLWithError = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>GoProxy Plus — 身份验证</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:linear-gradient(180deg,#f8fafc,#edf1f6);color:#172033;font-family:Inter,-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;padding:24px}
.card{border:1px solid #e1e6ee;padding:42px;width:min(420px,100%);background:rgba(255,255,255,.94);border-radius:22px;box-shadow:0 24px 70px rgba(29,43,70,.12)}
h1{font-size:30px;font-weight:750;margin-bottom:6px;letter-spacing:-.035em;color:#172033}.sub{color:#758196;font-size:12px;margin-bottom:28px;letter-spacing:.02em}
label{display:block;font-size:12px;color:#5d687a;margin-bottom:8px;font-weight:650}
input[type=password]{width:100%;padding:13px 14px;background:#fff;border:1px solid #d9e0ea;border-radius:10px;color:#172033;font-size:15px;outline:none;transition:border-color .16s ease,box-shadow .16s ease}input[type=password]:focus{border-color:#6789d8;box-shadow:0 0 0 4px rgba(50,102,213,.12)}
button{width:100%;margin-top:18px;padding:13px;background:#3266d5;color:#fff;border:0;border-radius:10px;font-size:13px;font-weight:700;cursor:pointer;transition:background .16s ease,transform .1s ease}button:hover{background:#2859bf}button:active{transform:scale(.98)}
.logo{display:inline-flex;align-items:center;justify-content:center;width:44px;height:44px;margin-bottom:22px;border-radius:13px;background:#edf3ff;color:#3266d5;font-size:14px;font-weight:800}.error{background:#fff1f3;color:#a92f42;padding:11px 12px;font-size:11px;margin-bottom:18px;border:1px solid #f1c8cf;border-radius:9px;font-weight:650}
.tip{color:#758196;font-size:11px;margin-top:22px;line-height:1.6;text-align:center}.tip a{color:#3266d5;text-decoration:none}.tip a:hover{text-decoration:underline}.github{position:absolute;top:22px;right:22px;color:#758196;transition:color .16s ease}.github:hover{color:#172033}
@media(prefers-reduced-motion:reduce){*{transition-duration:.01ms!important}}
</style>
</head>
<body>
<a href="https://github.com/Fiz2Z/GoProxy-Plus" target="_blank" class="github" title="GitHub">
  <svg width="32" height="32" viewBox="0 0 16 16" fill="currentColor">
    <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z"/>
  </svg>
</a>
<div class="card">
  <div class="logo">GP+</div>
  <h1>GoProxy Plus</h1>
  <p class="sub">智能代理池管理</p>
  <div class="error">密码不正确，请重新输入</div>
  <form method="POST" action="/login">
    <label>管理密码</label>
    <input type="password" name="password" placeholder="****************" autofocus>
    <button type="submit">登录控制台</button>
  </form>
  <p class="tip">访客模式可<a href="/">查看数据</a>，管理员登录后可完全控制</p>
</div>
</body>
</html>`

// dashboardHTML 已移至 dashboard.go
