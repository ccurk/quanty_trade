import requests
import time
import json

BASE_URL = "http://localhost:8080/api"
ADMIN_USER = "admin"
ADMIN_PASS = "admin123"

def test_flow():
    print("--- 🚀 开始量化交易系统接口自动化测试 ---")
    
    session = requests.Session()
    session.trust_env = False # Disable proxies
    
    # 1. Login Admin
    print("\n[1] 登录测试 (Admin)...", end=" ")
    login_res = session.post(f"http://localhost:8080/api/login", json={
        "username": ADMIN_USER,
        "password": ADMIN_PASS
    })
    if login_res.status_code != 200:
        print(f"FAILED: {login_res.text}")
        return
    token = login_res.json()["token"]
    headers = {"Authorization": f"Bearer {token}"}
    print("SUCCESS ✅")

    # 2. List Strategies (Initial)
    print("[2] 获取我的策略列表...", end=" ")
    strat_res = session.get(f"{BASE_URL}/strategies", headers=headers)
    if strat_res.status_code == 200:
        strats = strat_res.json()
        print(f"SUCCESS (找到 {len(strats)} 个策略) ✅")
    else:
        print(f"FAILED: {strat_res.text}")

    # 3. List Templates (Square)
    print("[3] 获取策略广场模板...", end=" ")
    temp_res = session.get(f"{BASE_URL}/templates", headers=headers)
    if temp_res.status_code == 200:
        temps = temp_res.json()
        print(f"SUCCESS (找到 {len(temps)} 个模板) ✅")
    else:
        print(f"FAILED: {temp_res.text}")

    # 4. Admin: Create New User
    trader_name = f"trader_{int(time.time())}"
    print(f"[4] 管理员创建新用户 ({trader_name})...", end=" ")
    user_res = session.post(f"{BASE_URL}/admin/users", headers=headers, json={
        "username": trader_name,
        "password": "password123",
        "role": "user"
    })

    if user_res.status_code == 200:
        print("SUCCESS ✅")
    else:
        print(f"FAILED: {user_res.text}")

    # 5. Admin: List All Users
    print("[5] 管理员查看用户列表...", end=" ")
    users_res = session.get(f"{BASE_URL}/admin/users", headers=headers)
    if users_res.status_code == 200:
        print(f"SUCCESS (共 {len(users_res.json())} 个用户) ✅")
    else:
        print(f"FAILED: {users_res.text}")

    # 6. Publish a Template (as Admin)
    print("[6] 发布策略到广场...", end=" ")
    pub_res = session.post(f"{BASE_URL}/templates/publish", headers=headers, json={
        "name": "均线趋势策略 v1",
        "description": "基于 20 日均线的经典趋势策略",
        "path": "../strategies/simple_trend.py"
    })
    if pub_res.status_code == 200:
        template_id = pub_res.json()["id"]
        print(f"SUCCESS (ID: {template_id}) ✅")
    else:
        print(f"FAILED: {pub_res.text}")
        return

    # 7. Reference Template (as Admin)
    print("[7] 从广场引用策略到我的策略...", end=" ")
    ref_res = session.post(f"{BASE_URL}/templates/reference", headers=headers, json={
        "template_id": template_id,
        "name": "我的比特币趋势策略",
        "config": json.dumps({"symbol": "BTC/USDT", "window": 10})
    })
    if ref_res.status_code == 200:
        strat_id = ref_res.json()["id"]
        print(f"SUCCESS (实例 ID: {strat_id}) ✅")
    else:
        print(f"FAILED: {ref_res.text}")
        return

    # 8. Start Strategy
    print(f"[8] 启动策略 (ID: {strat_id})...", end=" ")
    start_res = session.post(f"{BASE_URL}/strategies/{strat_id}/start", headers=headers)
    if start_res.status_code == 200:
        print("SUCCESS ✅")
    else:
        print(f"FAILED: {start_res.text}")

    time.sleep(2) # 等待运行一会

    # 9. Stop Strategy
    print(f"[9] 停止策略 (ID: {strat_id})...", end=" ")
    stop_res = session.post(f"{BASE_URL}/strategies/{strat_id}/stop", headers=headers)
    if stop_res.status_code == 200:
        print("SUCCESS ✅")
    else:
        print(f"FAILED: {stop_res.text}")


    print("\n--- ✨ 所有核心接口测试完成! ---")

if __name__ == "__main__":
    try:
        test_flow()
    except Exception as e:
        print(f"\n❌ 测试中断: {str(e)}")
