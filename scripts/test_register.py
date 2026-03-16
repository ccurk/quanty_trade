import requests
import json

BASE_URL = "http://localhost:8080/api"

def test_registration():
    print("--- 🚀 开始测试用户注册功能 ---")
    
    session = requests.Session()
    session.trust_env = False
    
    # 1. Register a new user with Binance configs
    username = "trader_new"
    password = "password123"
    configs = {
        "binance": {
            "apiKey": "test-api-key",
            "apiSecret": "test-api-secret"
        }
    }
    
    print(f"[1] 正在注册新用户 {username}...", end=" ")
    reg_res = session.post(f"{BASE_URL}/register", json={
        "username": username,
        "password": password,
        "configs": configs
    })
    
    if reg_res.status_code == 200:
        print("SUCCESS ✅")
    else:
        print(f"FAILED: {reg_res.status_code} - {reg_res.text}")
        return

    # 2. Login as the new user
    print(f"[2] 正在登录新用户 {username}...", end=" ")
    login_res = session.post(f"{BASE_URL}/login", json={
        "username": username,
        "password": password
    })
    
    if login_res.status_code == 200:
        user_data = login_res.json()["user"]
        print(f"SUCCESS ✅")
        print(f"    用户角色: {user_data.get('role')}")
        print(f"    交易所配置: {user_data.get('configs')}")
    else:
        print(f"FAILED: {login_res.status_code} - {login_res.text}")

    print("\n--- ✨ 注册与登录流程验证完成! ---")

if __name__ == "__main__":
    try:
        test_registration()
    except Exception as e:
        print(f"\n❌ 测试过程中出错: {str(e)}")
