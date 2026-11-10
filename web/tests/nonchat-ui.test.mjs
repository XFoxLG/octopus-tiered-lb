import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';
import React from 'react';
import * as jsx from 'react/jsx-runtime';
import { renderToStaticMarkup } from 'react-dom/server';
import * as intl from 'next-intl';
import * as icons from 'lucide-react';

export function nonChatComponents() {
 const compile=(file,deps={})=>{const m={exports:{}};vm.runInNewContext(ts.transpileModule(fs.readFileSync(new URL(file,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022,jsx:ts.JsxEmit.ReactJSX}}).outputText,{exports:m.exports,module:m,require:name=>{if(!(name in deps))throw Error(`Unexpected dependency ${name}`);return deps[name];}});return m.exports;};
 const deps={'react/jsx-runtime':jsx,'next-intl':intl,'lucide-react':icons,'@/api/nonchat':compile('../src/api/nonchat.ts')};
 return {...compile('../src/components/modules/log/NonChatUsage.tsx',deps),...compile('../src/components/common/NonChatStats.tsx',deps)};
}
export function renderNonChat(element,locale='zh_hans') { const messages=JSON.parse(fs.readFileSync(new URL(`../public/locale/${locale}.json`,import.meta.url),'utf8'));return renderToStaticMarkup(React.createElement(intl.NextIntlClientProvider,{locale:locale==='zh_hans'?'zh-CN':locale==='zh_hant'?'zh-TW':'en',messages,timeZone:'UTC'},element)); }
const {NonChatUsage,NonChatStats}=nonChatComponents();
const partial={basis:'processing',completeness:'complete',input_tokens:451,total_tokens:451,counts:{embedding_input_items:8,embedding_vectors:8,embedding_dimensions:4096},first_byte_ms:0,response_ms:1000};
for(const locale of ['en','zh_hans','zh_hant']) test(`non-chat UI uses real ${locale} labels, keeps unknown separate from zero`,()=>{
 const html=renderNonChat(React.createElement(NonChatUsage,{summary:partial,requestMS:2000,details:true}),locale);
 assert.ok(html.includes('4096')||html.includes('4,096'));
 assert.ok(html.includes('0ms'));assert.ok(html.includes('1.00s'));assert.ok(html.includes('4.00'));assert.ok(html.includes('225.50'));
 const messages=JSON.parse(fs.readFileSync(new URL(`../public/locale/${locale}.json`,import.meta.url),'utf8')).nonChat;
 assert.ok(html.includes(messages.notReported));assert.ok(!html.includes('MISSING_MESSAGE'));assert.ok(!html.includes('TPS'));
 const zero=renderNonChat(React.createElement(NonChatUsage,{summary:{...partial,output_tokens:0},requestMS:2000,details:true}),locale);
 assert.ok(zero.includes(`${messages.output}: 0`));
});
test('total-only and billed counters remain visible without invented direction',()=>{
 const html=renderNonChat(React.createElement(NonChatUsage,{summary:{basis:'billed',completeness:'complete',total_tokens:1000,counts:{rerank_documents:30,rerank_results:0}},evidence:{processing:{},billed:{total_tokens:1000},units:{search_units:1},response_state:'complete'},requestMS:2000,details:true}),'en');
 assert.match(html,/1,000/);assert.match(html,/15.00/);assert.match(html,/Search units/);assert.match(html,/not reported/i);
});
test('aggregates show observed zero and coverage, and omit unknown history',()=>{
 assert.equal(renderNonChat(React.createElement(NonChatStats,{metrics:{}})), '');
 const html=renderNonChat(React.createElement(NonChatStats,{metrics:{rerank_results:0,rerank_results_observed:2,unattributed_token:300,usage_reported_requests:2}}),'en');
 assert.match(html,/>0<\/strong>/);assert.match(html,/300/);assert.match(html,/2/);
});
