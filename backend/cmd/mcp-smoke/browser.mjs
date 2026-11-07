// Native Playwright driver for the local SDK smoke. Private input arrives only
// through stdin; no trace, video, request bodies, cookies or credentials persist.
import assert from 'node:assert/strict';
import {mkdir} from 'node:fs/promises';
import {pathToFileURL} from 'node:url';
import path from 'node:path';
let input='';for await(const chunk of process.stdin)input+=chunk;
const data=JSON.parse(input);input='';
let browser;
try{
 const {chromium}=await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE||path.resolve('node_modules/playwright-core/index.mjs')).href);
 browser=await chromium.launch({headless:true,executablePath:process.env.BROWSER_PATH,args:['--no-sandbox']});
 const context=await browser.newContext({viewport:{width:390,height:844}});
 const page=await context.newPage();page.setDefaultTimeout(20000);
 const errors=[];page.on('pageerror',()=>errors.push('browser page error'));
 const target=new URL(data.url,data.base).href;
 assert.equal(new URL(target).origin,data.base);
 await page.goto(target);
 await page.waitForURL(url=>url.pathname==='/admin/login/');
 await page.getByLabel('用户名',{exact:true}).fill('admin');
 await page.getByLabel('密码',{exact:true}).fill(data.password);
 await page.getByRole('button',{name:'登录',exact:true}).click();
 data.password='';
 await page.waitForURL(target);
 let result={original_url:target,login_return_url:page.url(),action:data.action};
 await mkdir(data.out,{recursive:true});
 if(data.action==='upload'){
  let selectedMediaID;
  page.on('framenavigated',frame=>{
   if(frame===page.mainFrame())selectedMediaID=new URL(frame.url()).searchParams.get('media_id')||selectedMediaID;
  });
  const uploaded=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname==='/api/admin/media');
  await page.getByLabel('上传图片',{exact:true}).setInputFiles({name:'sdk-smoke.png',mimeType:'image/png',buffer:Buffer.from(data.png,'base64')});
  await page.getByRole('button',{name:'上传并选择',exact:true}).click();
  const response=await uploaded;assert.equal(response.status(),201);
  // Navigation can discard the upload response body. Read the actual media_id
  // passed by the page into the editor, then resolve metadata with list_media.
  await page.waitForURL(url=>url.pathname==='/admin/products/edit/');
  await page.locator('input[type=password]').count().then(n=>assert.equal(n,0));
  await page.screenshot({path:path.join(data.out,'upload-login-return.png'),fullPage:false});
  result.media_id=selectedMediaID;
  assert.ok(result.media_id);
 }else if(data.action==='confirm'){
  await page.locator('#request-detail').waitFor({state:'visible'});
  assert.ok((await page.locator('#request-meta').textContent()).includes(data.request_id));
  assert.ok((await page.locator('#request-meta').textContent()).includes(data.sha256));
  await page.screenshot({path:path.join(data.out,'review-before-confirm.png'),fullPage:false});
  await page.locator('#reviewed-workspace').check();
  const confirmation=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname===`/api/admin/publication-requests/${data.request_id}/confirm`);
  await page.locator('#confirm-publication').click();
  const response=await confirmation;assert.equal(response.status(),202);
  result.submitted=JSON.parse(response.request().postData());
  assert.deepEqual(result.submitted,{snapshot_sha256:data.sha256,expected_active_release_id:data.expected_active,expected_workspace_revision:data.revision});
  await page.locator('#publication-result').waitFor({state:'visible',timeout:30000});
  result.visible_release=data.release_id;
  assert.ok((await page.locator('#publication-result').textContent()).includes(data.release_id));
  await page.screenshot({path:path.join(data.out,'confirmed-release.png'),fullPage:false});
 }else throw new Error('unsupported browser action');
 assert.deepEqual(errors,[]);result.page_errors=errors;
 console.log(JSON.stringify(result));
 await context.close();
}catch{
 // Playwright failures can include filled values or HTTP data. Emit a fixed
 // diagnostic; no automatic failure screenshot while a login is in progress.
 console.error('Native browser action failed; credentials and page data suppressed.');process.exitCode=1;
}finally{if(browser)await browser.close()}
