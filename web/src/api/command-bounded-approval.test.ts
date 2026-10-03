import { afterEach, describe, expect, it, vi } from "vitest";
import { CyberAgentClient } from "./client";

afterEach(() => vi.unstubAllGlobals());
const request = {version:"approval_control.v1",action:"approve_for_run",grant_ttl_seconds:120,grant_max_uses:2} as const;
const grant = {id:"grant-1",scope_fingerprint:"a".repeat(64),ttl_seconds:120,max_uses:2,uses_remaining:1,use_ordinal:1,expires_at:"2026-10-03T20:00:00Z",each_command_requires_review:true};
const decision = {version:"approval_control.v1",run_id:"run-1",approval_id:"approval-1",proposal_id:"call-1",tool_name:"command_runtime",action:"approve_for_run",status:"approved",replayed:false,
  process_execution_enabled:false,shell_execution_enabled:false,docker_execution_enabled:false,workspace_write_applied:false,session_grant_created:true,capability_grant:false,
  execution_resumed:false,retry_completed:false,retry_scheduled:false,bounded_grant:grant,continuation:{state:"completed",replayed:false,model_called:true,tool_called:true}};
function clientReturning(data: unknown) {
  const fetch=vi.fn(async()=>new Response(JSON.stringify({version:"api.v1",request_id:"bounded-approval",data}),{status:202,headers:{"Content-Type":"application/json"}}));
  vi.stubGlobal("fetch",fetch);
  return {client:new CyberAgentClient("read","/api/v1","control"),fetch};
}

describe("bounded command approval through the strict client",()=>{
  it("sends only explicit limits and accepts an exact consumed grant projection",async()=>{
    const {client,fetch}=clientReturning(decision);
    const result=await client.decideApproval("run-1","approval-1",request,"bounded-review-operation-1");
    expect(result.bounded_grant).toEqual(grant);
    const options=fetch.mock.calls[0] as unknown as [string,RequestInit];
    expect(JSON.parse(String(options[1].body))).toEqual(request);
  });
  it.each([{grant_ttl_seconds:0},{grant_ttl_seconds:901},{grant_max_uses:0},{grant_max_uses:9}])("rejects invalid limits before HTTP: %j",async(change)=>{
    const {client,fetch}=clientReturning(decision);
    await expect(client.decideApproval("run-1","approval-1",{...request,...change},"bounded-review-operation-1")).rejects.toThrow("explicit limits");
    expect(fetch).not.toHaveBeenCalled();
  });
  it.each([{tool_name:"shell"},{replayed:true},{bounded_grant:{...grant,each_command_requires_review:false}},{bounded_grant:{...grant,use_ordinal:2}},{bounded_grant:{...grant,ttl_seconds:121}},{bounded_grant:{...grant,lease_id:"model-lease"}}])("rejects widened or inconsistent response: %j",async(change)=>{
    const {client}=clientReturning({...decision,...change});
    await expect(client.decideApproval("run-1","approval-1",request,"bounded-review-operation-1")).rejects.toThrow("invalid");
  });
  it("does not accept bounded authority on an approve-once response",async()=>{
    const {client}=clientReturning({...decision,action:"approve_once"});
    await expect(client.decideApproval("run-1","approval-1",{version:"approval_control.v1",action:"approve_once"},"bounded-review-operation-1")).rejects.toThrow("unexpectedly created a grant");
  });
});
