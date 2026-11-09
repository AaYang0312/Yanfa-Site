import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, stat, mkdir } from 'node:fs/promises';
import { resolve, sep, extname } from 'node:path';
import { pathToFileURL } from 'node:url';

const site=process.env.MATERIAL_SITE_DIR && resolve(process.env.MATERIAL_SITE_DIR);
test('built material catalog displays complete details, families, neutral photos and keyboard guide at desktop/mobile sizes',{skip:!site,timeout:90000},async t=>{
  const {chromium}=await import(process.env.PLAYWRIGHT_MODULE ? pathToFileURL(resolve(process.env.PLAYWRIGHT_MODULE)).href : 'playwright-core');
  const types={'.html':'text/html','.css':'text/css','.js':'application/javascript','.svg':'image/svg+xml','.png':'image/png','.webp':'image/webp','.jpg':'image/jpeg','.woff2':'font/woff2','.xml':'application/xml'};
  const server=createServer(async(req,res)=>{
    try {
      let file=resolve(site,`.${decodeURIComponent(new URL(req.url,'http://localhost').pathname)}`);
      if(file!==site && !file.startsWith(site+sep)) {res.writeHead(403).end();return;}
      if((await stat(file)).isDirectory()) file=resolve(file,'index.html');
      res.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');res.end(await readFile(file));
    }catch{res.writeHead(404).end();}
  });
  await new Promise(done=>server.listen(Number(process.env.MATERIAL_QA_PORT || 0),'127.0.0.1',done));t.after(()=>new Promise(done=>server.close(done)));
  const browser=await chromium.launch({headless:true,...(process.env.BROWSER_PATH?{executablePath:process.env.BROWSER_PATH}:{channel:'chrome'})});t.after(()=>browser.close());
  const origin=`http://127.0.0.1:${server.address().port}`;const page=await browser.newPage({viewport:{width:1440,height:1000}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/api/catalog/release',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({schema_version:1,release_id:'material_preview_20261109'})}));
  await page.goto(`${origin}/products/`);await page.locator('astro-island[ssr]').waitFor({state:'detached'});
  assert.equal(await page.locator('[data-card]').count(),68);
  assert.match(await page.locator('.catalog-intro').innerText(),/50 款工具、5 项配件、13 类枪钉/);
  assert.equal(await page.locator('[data-guide-open]').count(),0);
  await page.locator('[data-mega-toggle]').first().hover();
  const thumbnail=page.locator('.mega__thumb .product-placeholder').first();
  await thumbnail.waitFor({state:'visible'});
  assert.equal((await thumbnail.innerText()).trim(),'');
  const fitsThumbnail=await thumbnail.evaluate(el=>{
    const frame=el.parentElement.getBoundingClientRect();const icon=el.querySelector('svg').getBoundingClientRect();
    return icon.width>0 && icon.height>0 && icon.width<=frame.width && icon.height<=frame.height;
  });
  assert.equal(fitsThumbnail,true);
  await page.keyboard.press('Escape');
  const launcher=page.locator('.yg-launcher');
  assert.equal(await launcher.getAttribute('aria-label'),'工具导购');
  await launcher.focus();const box=await launcher.boundingBox();assert.equal(box.width,52);assert.equal(box.height,52);
  assert.equal(await launcher.locator('.yg-launcher-tooltip').evaluate(e=>getComputedStyle(e).visibility),'visible');
  await launcher.press('Enter');assert.equal(await launcher.getAttribute('aria-expanded'),'true');
  const input=page.locator('#guide-message');await input.fill('保留这段咨询');await input.press('Escape');
  assert.equal(await launcher.getAttribute('aria-expanded'),'false');assert.equal(await launcher.evaluate(e=>document.activeElement===e),true);
  await launcher.press('Space');assert.equal(await input.inputValue(),'保留这段咨询');
  await page.getByRole('button',{name:'关闭导购',exact:true}).click();
  const output=resolve(process.env.MATERIAL_QA_OUTPUT||'.work/product-display-qa');await mkdir(output,{recursive:true});
  await page.screenshot({path:resolve(output,'catalog-desktop.png')});
  for(const [slug,photos] of [['yf1206',0],['spider-workbench',0],['nail-j10',14],['nail-n',7]]) {
    await page.goto(`${origin}/products/${slug}/`);
    assert.ok(await page.locator('#features li').count()>0);
    assert.ok(await page.locator('#applications p').count()>0);
    if(!photos) {
      const placeholder=page.locator('.detail__media .product-placeholder');
      assert.equal((await placeholder.innerText()).trim(),'');
      assert.equal(await placeholder.getAttribute('role'),'img');
      assert.match(await placeholder.getAttribute('aria-label'),/暂无实物图/);
      assert.equal(await placeholder.locator('svg circle').count(),3);
      assert.equal(await placeholder.evaluate(el=>el.getAnimations({subtree:true}).length),0);
      assert.equal(await placeholder.locator('animate, animateTransform').count(),0);
    }
    else assert.equal(await page.locator('[data-gallery-thumb]').count(),photos);
    if(slug.startsWith('nail-')) assert.equal(await page.locator('#applications h2').innerText(),'适用工具与用途');
  }
  await page.goto(`${origin}/products/lithium/`);
  assert.equal(await page.locator('.series__grid > *').count(),3);
  assert.equal(await page.locator('meta[name="robots"]').getAttribute('content'),'noindex, follow');
  await page.setViewportSize({width:390,height:844});
  for(const slug of ['yf1206','nail-j10']) {
    await page.goto(`${origin}/products/${slug}/`);
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=document.documentElement.clientWidth));
    assert.equal(await page.locator('main h1').count(),1);
    await page.screenshot({path:resolve(output,`${slug}-mobile.png`)});
  }
  const sitemap=await page.request.get(`${origin}/sitemap.xml`);assert.equal(sitemap.status(),200);
  const xml=await sitemap.text();assert.match(xml,/\/products\/nail-j10\//);assert.doesNotMatch(xml,/\/products\/(lithium|parts|nail-staple)\//);
  assert.deepEqual(errors,[]);
});
