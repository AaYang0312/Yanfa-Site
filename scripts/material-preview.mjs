// Offline verification only: constructs a frozen preview from the reviewed
// snapshot and bundle. Never reads or modifies a running service or database.
import assert from 'node:assert/strict';
import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises';
import { resolve, dirname } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { spawn } from 'node:child_process';
import { hashValue, planExisting } from './migrate-materials.mjs';

export function previewSnapshot(base,manifest) {
  const snapshot=structuredClone(base);
  const assets=new Map(manifest.assets.map(a=>[a.sha256,a]));
  for(const r of [...manifest.records,...manifest.hubs]) {
    if(r.action==='preserve') continue;
    const old=snapshot.products.find(p=>p.id===r.id);
    const product=structuredClone(r.product);
    if(r.action==='delete-hub') {
      assert.ok(old,`Missing preview hub: ${r.slug}`);
      const plan=planExisting(old,{...r,product:{}});assert.notEqual(plan.status,'conflict',`Preview hub changed: ${r.slug}`);
      snapshot.products=snapshot.products.filter(p=>p.id!==r.id);
      if(!snapshot.previously_published_routes.some(route=>route.slug===r.slug)) snapshot.previously_published_routes.push({product_id:r.id,slug:r.slug});
      continue;
    }
    if(r.images.length) {
      product.images=r.images.map(image=>{
        const a=assets.get(image.sha256);
        return {media_id:`m_${a.sha256}`,label:image.label,description:image.description,sha256:a.sha256,format:a.format,bytes:a.bytes,width:a.width,height:a.height,path:`${a.sha256}.jpg`};
      });
      product.cover_media_id=product.images[0].media_id;
    }
    if(old) {
      // Do not use the helper to erase baseline conflicts in a different snapshot.
      const plan=planExisting(old,{...r,product:{...product,images:product.images?.map(({media_id,label,description})=>({media_id,label,description})) || old.images.map(({media_id,label,description})=>({media_id,label,description}))}});
      assert.notEqual(plan.status,'conflict',`Preview baseline changed: ${r.slug} ${plan.conflicts}`);
      Object.assign(old,product);
      for(const source of old.guide_profile.sources) if(source.kind==='product_text') {
        const field=source.reference.slice(1); assert.ok(field in old,'Unsupported nested source'); source.sha256=hashValue(old[field]);
      }
    } else {
      snapshot.products.push({id:`preview_${r.slug.replaceAll('-','_')}`,slug:r.slug,...product,guide_profile:{...product.guide_profile,reviews:[]},version:1,updated_at:'2026-11-09T00:00:00Z'});
    }
  }
  snapshot.release_id='material_preview_20261109';
  const listed=snapshot.products.filter(p=>p.visibility==='listed');
  assert.equal(listed.length,68);
  assert.deepEqual(Object.fromEntries(['tool','entry','consumable'].map(kind=>[kind,listed.filter(p=>p.kind===kind).length])),{tool:50,entry:5,consumable:13});
  for(const r of manifest.records.filter(r=>r.action==='preserve')) assert.deepEqual(snapshot.products.find(p=>p.id===r.id),base.products.find(p=>p.id===r.id));
  return snapshot;
}
export async function buildMaterialPreview({snapshot:input,output,root=resolve('.'),manifest:manifestPath=resolve(root,'scripts/material-manifest.json')}) {
  const {copyTemplate}=await import('./build-candidate.mjs');
  const {readSnapshot,mediaFilename,auditSite}=await import('./audit-build.mjs');
  const bundle=JSON.parse(await readFile(manifestPath,'utf8'));
  const snapshot=previewSnapshot(JSON.parse(await readFile(input,'utf8')),bundle);
  const work=resolve(output);await mkdir(work,{recursive:true});
  const frozen=resolve(work,'snapshot.json');await writeFile(frozen,JSON.stringify(snapshot,null,2)+'\n');
  readSnapshot(frozen);
  const template=await copyTemplate(root,resolve(work,'template'));
  const mediaDir=resolve(template,'src/assets/catalog-media'); const publicDir=resolve(template,'public/media');
  await mkdir(mediaDir,{recursive:true});await mkdir(publicDir,{recursive:true});
  const oldMedia=JSON.parse(await readFile(resolve(root,'seed/media-manifest.json'),'utf8')).media;
  const copies=new Map();
  for(const p of snapshot.products.filter(p=>p.visibility==='listed')) for(const image of p.images) copies.set(image.sha256,image);
  for(const image of copies.values()) {
    const accepted=bundle.assets.find(a=>a.sha256===image.sha256);
    const old=oldMedia.find(a=>a.sha256===image.sha256);assert.ok(accepted||old);
    const source=accepted?resolve(dirname(manifestPath),accepted.file):resolve(root,'src/assets/products',old.path);
    const bytes=await readFile(source);assert.equal(hashValue(bytes),image.sha256);
    await copyFile(source,resolve(mediaDir,mediaFilename(image)));await copyFile(source,resolve(publicDir,mediaFilename(image)));
  }
  const site=resolve(work,'site');
  await new Promise((done,reject)=>{
    const child=spawn(process.execPath,[resolve(root,'node_modules/astro/astro.js'),'build','--root',template],{cwd:template,stdio:'inherit',env:{...process.env,CATALOG_SNAPSHOT:frozen,CATALOG_MEDIA_DIR:mediaDir,BUILD_SITE_DIR:site,ASTRO_TELEMETRY_DISABLED:'1'}});
    child.on('error',reject);child.on('exit',code=>code===0?done():reject(Error(`Astro exited ${code}`)));
  });
  const {routes,resources,...report}=await auditSite(frozen,site);
  return {...report,site,snapshot:frozen};
}
if(process.argv[1] && import.meta.url===pathToFileURL(resolve(process.argv[1])).href) {
  const {values}=parseArgs({options:{snapshot:{type:'string'},out:{type:'string'},manifest:{type:'string',default:fileURLToPath(new URL('./material-manifest.json',import.meta.url))}}});
  assert.ok(values.snapshot && values.out,'--snapshot and a fresh --out directory are required');
  console.log(JSON.stringify(await buildMaterialPreview({snapshot:values.snapshot,output:values.out,manifest:values.manifest}),null,2));
}
